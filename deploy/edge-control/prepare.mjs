// Generates deployment configuration; no credentials are printed.
import {execFileSync} from 'node:child_process';
import {readFileSync,writeFileSync} from 'node:fs';
import {generateKeyPairSync,randomBytes,createHash} from 'node:crypto';
import {join,isAbsolute} from 'node:path';
const [mode,dir]=process.argv.slice(2);if(!isAbsolute(dir||''))throw Error('private directory required');
const ssh=(host,cmd,input)=>execFileSync('ssh',['-o','ConnectTimeout=10',host,cmd],{input,maxBuffer:8<<20,stdio:['pipe','pipe','pipe']}).toString();
const save=(name,data)=>writeFileSync(join(dir,name),data,{mode:0o600,flag:'wx'});
const sql=q=>ssh('ali98',`podman exec -i sub2api-postgres sh -c 'exec psql -X -v ON_ERROR_STOP=1 -At -U "$POSTGRES_USER" -d "$POSTGRES_DB"'`,q);
try{
 if(mode==='generate'){
  const {privateKey,publicKey}=generateKeyPairSync('ed25519');const token=randomBytes(32).toString('hex');
  save('signing.pem',privateKey.export({format:'pem',type:'pkcs8'}));save('public.pem',publicKey.export({format:'pem',type:'spki'}));
  save('control.json',JSON.stringify({token,key_file:'/app/edge-state/signing.pem',state_dir:'/app/edge-state/leases'}));
  save('gateway.json',JSON.stringify({main_url:'http://127.0.0.1:29445',token,public_key:'/opt/lklb-us-gateway/public.pem',state_dir:'/var/lib/lklb-us-gateway/requests'}));
  const raw=JSON.parse(ssh('ali98','podman inspect sub2api --format "{{json .Config.Env}}"'));
  const env=Object.fromEntries(raw.map(s=>[s.slice(0,s.indexOf('=')),s.slice(s.indexOf('=')+1)]).filter(([k])=>/^(DATABASE_|REDIS_|JWT_|TOTP_|GATEWAY_|SECURITY_|PROMPT_|BILLING_|API_KEY_|SUBSCRIPTION_|OPS_|DEFAULT_|PRICING_|SERVER_|TIMEZONE$|TZ$|RUN_MODE$)/.test(k)));
  Object.assign(env,{LKLB_EDGE_CONTROL_ONLY:'1',LKLB_EDGE_CONTROL_CONFIG:'/app/edge-state/control.json',SERVER_HOST:'0.0.0.0',SERVER_PORT:'8080',RUN_MODE:'standard',LOG_OUTPUT_TO_FILE:'false',LOG_OUTPUT_TO_STDOUT:'true',LOG_LEVEL:'warn',PRICING_DATA_DIR:'/app/data',GATEWAY_USAGE_RECORD_WORKER_COUNT:'2',GATEWAY_USAGE_RECORD_QUEUE_SIZE:'32',GATEWAY_USAGE_RECORD_AUTO_SCALE_ENABLED:'false',GOMEMLIMIT:'192MiB',GOGC:'50',GOMAXPROCS:'1'});
  for(const[k,v]of Object.entries(env))if(!/^[A-Z][A-Z0-9_]*$/.test(k)||/[\r\n\0]/.test(v))throw Error('invalid env');
  save('control.env',Object.entries(env).map(([k,v])=>`${k}=${v}`).join('\n')+'\n');
  save('known_hosts',execFileSync('ssh-keygen',['-F','139.224.110.98'],{stdio:['ignore','pipe','pipe']}));
  console.log('Control credentials generated; database credentials remain on the main host only.');
 }else if(mode==='authorize'){
  const pub=ssh('bwg','cat /etc/lklb-us-gateway/link-key.pub').trim();if(!/^ssh-ed25519 [A-Za-z0-9+/=]+(?: [^\r\n]*)?$/.test(pub))throw Error('invalid public key');
  const old=ssh('ali98','cat /home/admin/.ssh/authorized_keys');if(old.includes(pub.split(' ')[1]))throw Error('already authorized');
  const line=`from="45.78.79.47",restrict,port-forwarding,permitopen="127.0.0.1:18445",command="/bin/false" ${pub}\n`;
  save('authorized_keys.before',old);save('authorized_keys.next',old+(old.endsWith('\n')?'':'\n')+line);
  execFileSync('scp',[join(dir,'authorized_keys.next'),'ali98:/home/admin/.ssh/authorized_keys.edge-next'],{stdio:['ignore','pipe','pipe']});
  const hash=createHash('sha256').update(old).digest('hex');
  ssh('ali98',`test "$(sha256sum /home/admin/.ssh/authorized_keys | cut -d' ' -f1)" = '${hash}' && chmod 600 /home/admin/.ssh/authorized_keys.edge-next && mv /home/admin/.ssh/authorized_keys.edge-next /home/admin/.ssh/authorized_keys`);
  console.log('Restricted control-only SSH key installed; other SSH keys preserved.');
 }else if(mode==='test-key'){
  const row=JSON.parse(sql(`SELECT row_to_json(k) FROM (SELECT id,key FROM api_keys WHERE user_id=1 AND status='active' AND deleted_at IS NULL AND group_id=2 AND quota=0 AND (expires_at IS NULL OR expires_at>NOW()) ORDER BY id LIMIT 1) k;`).trim());save('admin-key',row.key);console.log(`Test key ID ${row.id}; secret not printed.`);
 }else{throw Error('unknown operation')}
}catch{console.error('Preparation failed; credential-bearing output withheld.');process.exitCode=1;}
