import {execFileSync} from 'node:child_process';
const sql=q=>execFileSync('ssh',['ali98',`podman exec -i sub2api-postgres sh -c 'exec psql -X -v ON_ERROR_STOP=1 -At -U "$POSTGRES_USER" -d "$POSTGRES_DB"'`],{input:q,stdio:['pipe','pipe','pipe'],maxBuffer:1<<20}).toString().trim();
const getKey=(where)=>JSON.parse(sql(`SELECT row_to_json(k) FROM (SELECT id,key FROM api_keys WHERE ${where} AND deleted_at IS NULL ORDER BY id LIMIT 1) k;`));
try{
 const key=getKey("user_id=157 AND status='active' AND group_id=2");
 const base='https://us.lklb.top';
 const model=await fetch(base+'/v1/models',{headers:{Authorization:'Bearer '+key.key},signal:AbortSignal.timeout(20000)});await model.arrayBuffer();
 const started=Date.now();const resp=await fetch(base+'/v1/responses',{method:'POST',headers:{Authorization:'Bearer '+key.key,'Content-Type':'application/json','User-Agent':'codex_cli_rs/0.144.1'},body:JSON.stringify({model:'gpt-5.6-sol',input:'Reply exactly OK.',reasoning:{effort:'low'},stream:true}),signal:AbortSignal.timeout(90000)});
 const id=resp.headers.get('x-lklb-edge-request-id');const text=await resp.text();const complete=/"type"\s*:\s*"response.completed"/.test(text);
 console.log(JSON.stringify({user_id:157,key_id:key.id,models_status:model.status,response_status:resp.status,complete,edge_id:id,seconds:(Date.now()-started)/1000}));
 if(!id||!/^[a-f0-9]{32}$/.test(id)||resp.status!==200||!complete)throw Error('live request failed');
 let rows;
 for(let i=0;i<12;i++){
  rows=JSON.parse(sql(`SELECT COALESCE(json_agg(t),'[]'::json) FROM (SELECT user_id,api_key_id,actual_cost,rate_multiplier,request_id FROM usage_logs WHERE request_id='edge:${id}') t;`));
  if(rows.length)break;await new Promise(r=>setTimeout(r,500));
 }
 if(rows.length!==1||rows[0].user_id!==157||rows[0].api_key_id!==key.id||Number(rows[0].actual_cost)<=0)throw Error('billing attribution failed');
 console.log(JSON.stringify({billing:rows[0]}));
 const inactive=getKey("user_id=157 AND status='inactive' AND group_id=2");
 const other=getKey("user_id NOT IN (1,157) AND status='active' AND group_id=2");
 const admin=getKey("user_id=1 AND status='active' AND group_id=2");
 for(const[name,k,expected]of [['inactive',inactive,401],['other_user',other,403],['admin',admin,200]]){
  const r=await fetch(base+'/v1/models',{headers:{Authorization:'Bearer '+k.key},signal:AbortSignal.timeout(20000)});await r.arrayBuffer();console.log(JSON.stringify({check:name,status:r.status}));if(r.status!==expected)throw Error('access validation failed');
 }
}catch{console.error('Verification failed; credentials and raw response withheld.');process.exitCode=1}
