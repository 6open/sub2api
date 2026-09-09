import {execFileSync} from 'node:child_process';
import {readFileSync,writeFileSync} from 'node:fs';
import {join} from 'node:path';
import {randomUUID} from 'node:crypto';
const[mode,dir,model='gpt-5.6-sol']=process.argv.slice(2);
const key=readFileSync(join(dir,'admin-key'),'utf8').trim();if(!/^[A-Za-z0-9_-]+$/.test(key))throw Error('invalid key');
const base=mode.startsWith('public')?'https://us.lklb.top:9443':mode.startsWith('baseline')?'https://lklb.top':'http://127.0.0.1:19445';
const models=mode.endsWith('models');const id=randomUUID();
const body={model,input:'Reply exactly OK.',reasoning:{effort:'low'},stream:true};
if(mode.includes('tool')){body.input='Call echo_probe with value OK.';body.tools=[{type:'function',name:'echo_probe',description:'Echo a value',parameters:{type:'object',properties:{value:{type:'string'}},required:['value'],additionalProperties:false}}];body.tool_choice={type:'function',name:'echo_probe'}}
const config=[`url = ${JSON.stringify(base+(models?'/v1/models':'/v1/responses'))}`,`header = ${JSON.stringify('Authorization: Bearer '+key)}`,`header = "User-Agent: codex_cli_rs/0.144.1"`,`header = "Content-Type: application/json"`];
if(!models)config.push(`data = ${JSON.stringify(JSON.stringify(body))}`);
try{
 const raw=execFileSync('ssh',['bwg',`curl -sS --max-time 120 -D - --config - -w '\n__RESULT__%{http_code}|%{time_starttransfer}|%{time_total}'`],{input:config.join('\n')+'\n',stdio:['pipe','pipe','pipe'],maxBuffer:8<<20}).toString();
 const split=raw.lastIndexOf('\n__RESULT__');const[status,first,total]=raw.slice(split+11).split('|');const response=raw.slice(0,split);
 writeFileSync(join(dir,`probe-${id}.json`),JSON.stringify({mode,id,status,first,total,response}),{mode:0o600});
 const separator=response.indexOf('\r\n\r\n');const headers=response.slice(0,separator);const payload=response.slice(separator+4);
 const summary={mode,status:Number(status),first_byte_seconds:Number(first),total_seconds:Number(total),edge_id:headers.match(/^x-lklb-edge-request-id:\s*(\w+)/im)?.[1]};
 let parsed;try{parsed=JSON.parse(payload)}catch{}
 if(models)summary.models=parsed?.data?.map(m=>m.id);
 else{
  const events=payload.split('\n').filter(l=>l.startsWith('data: ')).map(l=>{try{return JSON.parse(l.slice(6))}catch{return null}}).filter(Boolean);
  const terminal=events.find(e=>e.type==='response.completed');summary.completed=!!terminal;summary.model=terminal?.response?.model;
  const usage=terminal?.response?.usage;summary.usage=usage?{input:usage.input_tokens,output:usage.output_tokens,cached:usage.input_tokens_details?.cached_tokens}:undefined;
  summary.has_text=events.some(e=>e.type==='response.output_text.delta'&&e.delta?.length>0);
  summary.tool_names=events.filter(e=>e.type==='response.output_item.done'&&e.item?.type==='function_call').map(e=>e.item.name);
  if(parsed?.error)summary.error=typeof parsed.error==='string'?parsed.error:String(parsed.error.message||parsed.error.type).replace(/sk-[A-Za-z0-9_-]+/g,'[redacted]').slice(0,250);
 }
 console.log(JSON.stringify(summary));
}catch{console.error('Probe failed; credentials and raw output withheld.');process.exitCode=1}
