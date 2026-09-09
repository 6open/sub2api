import {spawn,execFileSync} from 'node:child_process';
import {readFileSync,writeFileSync} from 'node:fs';
import {join} from 'node:path';
const dir=process.argv[2];
const key=readFileSync(join(dir,'admin-key'),'utf8').trim();
const cfg=JSON.parse(readFileSync(join(dir,'gateway.json')));
const ssh=(cmd,input)=>execFileSync('ssh',['bwg',cmd],{input,stdio:['pipe','pipe','pipe'],maxBuffer:8<<20}).toString();
let stopped=false,raw='',stage='stream';
try{
 const options=[`url = "http://127.0.0.1:19445/v1/responses"`,`header = ${JSON.stringify('Authorization: Bearer '+key)}`,`header = "User-Agent: codex_cli_rs/0.144.1"`,`header = "Content-Type: application/json"`,`data = ${JSON.stringify(JSON.stringify({model:'gpt-5.6-sol',input:'Write the integers from 1 to 100, one per line.',reasoning:{effort:'low'},stream:true}))}`];
 const child=spawn('ssh',['bwg','curl -N -sS --max-time 90 -D - --config -'],{stdio:['pipe','pipe','pipe']});
 child.stdout.on('data',chunk=>{
  raw+=chunk.toString();
  if(!stopped&&/x-lklb-edge-request-id:/i.test(raw)){
   stopped=true;ssh('systemctl stop lklb-us-link');
  }
 });
 child.stderr.on('data',()=>{});
 child.stdin.end(options.join('\n')+'\n');
 const code=await new Promise(resolve=>child.on('close',resolve));
 const id=raw.match(/^x-lklb-edge-request-id:\s*([a-f0-9]{32})/im)?.[1];
 if(code!==0||!id||!stopped||!raw.includes('response.completed'))throw Error('stream test failed');
 stage='queue';const before=JSON.parse(ssh('curl -sS --max-time 5 http://127.0.0.1:19445/health'));
 if(before.pending_settlements<1)throw Error('receipt was not queued');
 stage='restart';ssh('systemctl restart lklb-us-gateway');
 ssh('systemctl start lklb-us-link');
 stage='recover';let recovered=false;
 for(let i=0;i<30;i++){
  await new Promise(resolve=>setTimeout(resolve,500));
  try{const health=JSON.parse(ssh('curl -sS --max-time 5 http://127.0.0.1:19445/health'));if(health.pending_settlements===0){recovered=true;break}}catch{}
 }
 if(!recovered)throw Error('recovery not complete');
 stage='duplicate';const record=JSON.parse(ssh(`cat /var/lib/private/lklb-us-gateway/requests/${id}.json`));
 const retry=[`url = ${JSON.stringify(cfg.main_url+'/internal/lklb-edge/settle')}`,`header = ${JSON.stringify('X-Lklb-Node-Token: '+cfg.token)}`,`header = "Content-Type: application/json"`,`data = ${JSON.stringify(JSON.stringify(record.Receipt))}`];
 const duplicate=JSON.parse(ssh('curl -fsS --max-time 15 --config -',retry.join('\n')+'\n'));
 writeFileSync(join(dir,'fault-result.json'),JSON.stringify({id,stream_completed:true,queued:before.pending_settlements,recovered,duplicate_state:duplicate.state}),{mode:0o600});
 console.log(JSON.stringify({id,stream_completed:true,queued:before.pending_settlements,recovered,duplicate_state:duplicate.state}));
}catch(err){console.error(JSON.stringify({stage,stopped,error:String(err.message).split('\n')[0].slice(0,200)}));process.exitCode=1}
finally{try{ssh('systemctl start lklb-us-link')}catch{console.error('Unable to restore dedicated link');process.exitCode=1}}
