import {execFileSync} from 'node:child_process';
import {readFileSync} from 'node:fs';
const sql=q=>execFileSync('ssh',['ali98',`podman exec -i sub2api-postgres sh -c 'exec psql -X -v ON_ERROR_STOP=1 -At -U "$POSTGRES_USER" -d "$POSTGRES_DB"'`],{input:q,stdio:['pipe','pipe','pipe'],maxBuffer:1<<20}).toString().trim();
try{
 const key=JSON.parse(sql("SELECT row_to_json(k) FROM (SELECT id,key FROM api_keys WHERE user_id=1 AND status='active' AND deleted_at IS NULL AND group_id=2 AND quota=0 ORDER BY id LIMIT 1) k;"));
 const png=readFileSync('/home/lk/work/tools/sub2api/assets/partners/logos/byteplus.png').toString('base64');
 const base='https://us.lklb.top';
 const headers={Authorization:'Bearer '+key.key,'Content-Type':'application/json','User-Agent':'codex_cli_rs/0.144.1'};
 const body={model:'gpt-5.6-sol',reasoning:{effort:'low'},stream:true,input:[{role:'user',content:[{type:'input_image',image_url:'data:image/png;base64,'+png},{type:'input_text',text:'Keep this image in mind.'}]},{role:'user',content:[{type:'input_text',text:'Read the brand name shown in the earlier image. Reply with that name only.'}]}]};
 const start=Date.now();const r=await fetch(base+'/v1/responses',{method:'POST',headers,body:JSON.stringify(body),signal:AbortSignal.timeout(90000)});const raw=await r.text();
 const events=raw.split('\n').filter(s=>s.startsWith('data:')).map(s=>{try{return JSON.parse(s.slice(5))}catch{return null}}).filter(Boolean);
 const output=events.filter(e=>e.type==='response.output_text.delta').map(e=>e.delta).join('');const completed=events.find(e=>e.type==='response.completed');const id=r.headers.get('x-lklb-edge-request-id');
 console.log(JSON.stringify({status:r.status,completed:!!completed,image_recognized:/byteplus/i.test(output),request_bytes:Buffer.byteLength(JSON.stringify(body)),seconds:(Date.now()-start)/1000,edge_id:id,usage:completed?.response?.usage?{input:completed.response.usage.input_tokens,output:completed.response.usage.output_tokens,image:completed.response.usage.input_tokens_details?.image_tokens}:null}));
 if(r.status!==200||!completed||!/byteplus/i.test(output)||!id||!/^[a-f0-9]{32}$/.test(id))throw Error('vision verification failed');
 let rows=[];for(let i=0;i<12;i++){rows=JSON.parse(sql(`SELECT COALESCE(json_agg(t),'[]'::json) FROM (SELECT user_id,api_key_id,actual_cost,input_tokens,image_input_tokens,output_tokens FROM usage_logs WHERE request_id='edge:${id}') t;`));if(rows.length)break;await new Promise(r=>setTimeout(r,500));}
 console.log(JSON.stringify({billing:rows}));if(rows.length!==1||Number(rows[0].actual_cost)<=0)throw Error('billing missing');
 const large=JSON.stringify({...body,input:'x'.repeat(8*1024*1024)});const rejected=await fetch(base+'/v1/responses',{method:'POST',headers,body:large,signal:AbortSignal.timeout(30000)});await rejected.arrayBuffer();console.log(JSON.stringify({oversize_status:rejected.status}));if(rejected.status!==413)throw Error('oversize was not rejected');
}catch{console.error('Vision verification failed; credentials and raw output withheld.');process.exitCode=1}
