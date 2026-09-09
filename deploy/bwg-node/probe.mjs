import { readFileSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { join } from 'node:path';

const [mode, dir, model='gpt-5.6-sol'] = process.argv.slice(2);
const key=readFileSync(join(dir,'admin-test-key'),'utf8').trim();
if (!/^[A-Za-z0-9_-]+$/.test(key)) throw Error('invalid private test credential');
const requestID='bwg-'+randomUUID();
const base=mode.startsWith('public')?'https://us.lklb.top:9443':mode.startsWith('baseline')?'https://lklb.top':'http://127.0.0.1:19444';
const models=mode.endsWith('models');
const payload={model,input:'Reply exactly OK.',reasoning:{effort:'low'},stream:true};
const options=[`url = ${JSON.stringify(base+(models?'/v1/models':'/v1/responses'))}`,
  `header = ${JSON.stringify('Authorization: Bearer '+key)}`,
  `header = ${JSON.stringify('X-Client-Request-Id: '+requestID)}`,
  `header = "User-Agent: codex_cli_rs/0.144.1"`,
  `header = "Content-Type: application/json"`];
if(!models) options.push(`data = ${JSON.stringify(JSON.stringify(payload))}`);
try {
  const raw=execFileSync('ssh',['bwg',`curl -sS --max-time 150 --config - -w '\n__RESULT__%{http_code}|%{time_starttransfer}|%{time_total}'`],{input:options.join('\n')+'\n',maxBuffer:8<<20,stdio:['pipe','pipe','pipe']}).toString();
  const split=raw.lastIndexOf('\n__RESULT__');
  const body=raw.slice(0,split), [status,first,total]=raw.slice(split+11).split('|');
  writeFileSync(join(dir,`probe-${requestID}.json`),JSON.stringify({mode,requestID,status,first,total,body}),{mode:0o600});
  let parsed;
  try{parsed=JSON.parse(body)}catch{}
  const summary={mode,requestID,status:Number(status),first_byte_seconds:Number(first),total_seconds:Number(total)};
  if(models){summary.models=parsed?.data?.map(x=>x.id)}
  else {
    const events=body.split('\n').filter(l=>l.startsWith('data: ')).map(l=>{try{return JSON.parse(l.slice(6))}catch{return null}}).filter(Boolean);
    const complete=events.find(e=>e.type==='response.completed');
    summary.completed=!!complete;
    summary.response_model=complete?.response?.model;
    summary.usage=complete?.response?.usage;
    summary.has_output=events.some(e=>e.type==='response.output_text.delta'&&e.delta?.length>0);
    if(parsed?.error){summary.error_code=parsed.error.code;summary.error_type=parsed.error.type;summary.error_message=String(parsed.error.message||'').replace(/sk-[A-Za-z0-9_-]+/g,'[redacted]').slice(0,300)}
  }
  console.log(JSON.stringify(summary));
}catch{console.error('Probe failed; credential and raw output not printed.');process.exitCode=1;}
