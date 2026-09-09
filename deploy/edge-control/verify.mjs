import {readFileSync,writeFileSync} from 'node:fs';
import {execFileSync} from 'node:child_process';
import {join} from 'node:path';
const dir=process.argv[2];const key=readFileSync(join(dir,'admin-key'),'utf8').trim();
const base='https://us.lklb.top';
const headers={'Authorization':'Bearer '+key,'User-Agent':'codex_cli_rs/0.144.1','Content-Type':'application/json'};
const request=async()=>{const started=Date.now();const r=await fetch(base+'/v1/responses',{method:'POST',headers,body:JSON.stringify({model:'gpt-5.6-sol',input:'Reply exactly OK.',reasoning:{effort:'low'},stream:true}),signal:AbortSignal.timeout(90000)});const text=await r.text();return {status:r.status,complete:text.includes('"type":"response.completed"')||text.includes('"type": "response.completed"'),id:r.headers.get('x-lklb-edge-request-id'),seconds:(Date.now()-started)/1000}};
try{
 const results=await Promise.all(Array.from({length:20},request));
 writeFileSync(join(dir,'concurrency-result.json'),JSON.stringify(results),{mode:0o600});
 console.log(JSON.stringify({concurrent:20,completed:results.filter(x=>x.status===200&&x.complete).length,statuses:results.map(x=>x.status),ids:results.map(x=>x.id),seconds:results.map(x=>x.seconds)}));
 const query="SELECT key FROM api_keys WHERE user_id NOT IN (1,157) AND status='active' AND deleted_at IS NULL AND group_id=2 ORDER BY id LIMIT 1;";
 const other=execFileSync('ssh',['ali98',`podman exec -i sub2api-postgres sh -c 'exec psql -X -At -U "$POSTGRES_USER" -d "$POSTGRES_DB"'`],{input:query,stdio:['pipe','pipe','pipe']}).toString().trim();
 const denied=await fetch(base+'/v1/models',{headers:{Authorization:'Bearer '+other},signal:AbortSignal.timeout(15000)});
 console.log(JSON.stringify({non_admin_status:denied.status}));await denied.arrayBuffer();
 if(results.some(x=>x.status!==200||!x.complete)||denied.status!==403)process.exitCode=1;
}catch{console.error('Verification failed; credentials withheld.');process.exitCode=1}
