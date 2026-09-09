// Generates private deployment artifacts without printing credentials.
import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { randomBytes, createHash } from 'node:crypto';
import { join, isAbsolute } from 'node:path';

const [mode, dir] = process.argv.slice(2);
if (!dir || !isAbsolute(dir)) throw Error('private state directory required');
const ssh = (host, command, input) => execFileSync('ssh', ['-o', 'ConnectTimeout=10', host, command], { input, maxBuffer: 8 << 20, stdio: ['pipe', 'pipe', 'pipe'] }).toString();
const sql = (query) => ssh('ali98', `podman exec -i sub2api-postgres sh -c 'exec psql -X -v ON_ERROR_STOP=1 -At -U "$POSTGRES_USER" -d "$POSTGRES_DB"'`, query);
const save = (name, data) => writeFileSync(join(dir, name), data, { mode: 0o600, flag: 'wx' });

try {
  if (mode === 'capture') {
    const raw = JSON.parse(ssh('ali98', 'podman inspect sub2api --format "{{json .Config.Env}}"'));
    const env = Object.fromEntries(raw.map(s => [s.slice(0, s.indexOf('=')), s.slice(s.indexOf('=') + 1)]).filter(([k]) => /^(DATABASE_|REDIS_|JWT_|TOTP_|GATEWAY_|SECURITY_|BILLING_|API_KEY_AUTH_CACHE_|SUBSCRIPTION_|LOG_|DEFAULT_|TIMEZONE$|TZ$|RUN_MODE$)/.test(k) && !k.includes('ADMIN_PASSWORD')));
    const password = randomBytes(32).toString('hex');
    Object.assign(env, {
      LKLB_EDGE_NODE: '1', LKLB_EDGE_STATE_DIR: '/var/lib/lklb-bwg-node/data',
      DATABASE_HOST: '127.0.0.1', DATABASE_PORT: '25432', DATABASE_USER: 'lklb_bwg_node', DATABASE_PASSWORD: password,
      DATABASE_MAX_OPEN_CONNS: '4', DATABASE_MAX_IDLE_CONNS: '2', DATABASE_SSLMODE: 'disable',
      REDIS_HOST: '127.0.0.1', REDIS_PORT: '26379', REDIS_POOL_SIZE: '6', REDIS_MIN_IDLE_CONNS: '1',
      SERVER_HOST: '127.0.0.1', SERVER_PORT: '19444', SERVER_MODE: 'release', RUN_MODE: 'standard',
      SERVER_TRUSTED_PROXIES: '127.0.0.1/32,::1/128',
      JWT_SECRET: randomBytes(32).toString('hex'),
      OPS_ENABLED: 'false',
      PRICING_DATA_DIR: '/var/lib/lklb-bwg-node/data',
      GATEWAY_MAX_BODY_SIZE: String(8 << 20), GATEWAY_TEXT_MAX_BODY_SIZE: String(8 << 20),
      GATEWAY_UPSTREAM_RESPONSE_READ_MAX_BYTES: String(8 << 20),
      GATEWAY_OPENAI_WS_FORCE_HTTP: 'true',
      GATEWAY_MAX_UPSTREAM_CLIENTS: '16', GATEWAY_MAX_IDLE_CONNS: '16', GATEWAY_MAX_IDLE_CONNS_PER_HOST: '10', GATEWAY_MAX_CONNS_PER_HOST: '10',
      GATEWAY_USAGE_RECORD_WORKER_COUNT: '2', GATEWAY_USAGE_RECORD_QUEUE_SIZE: '32', GATEWAY_USAGE_RECORD_AUTO_SCALE_ENABLED: 'false',
      LOG_LEVEL: 'warn', LOG_OUTPUT_TO_FILE: 'false', LOG_OUTPUT_TO_STDOUT: 'true',
    });
    for (const k of Object.keys(env)) if (!/^[A-Z][A-Z0-9_]*$/.test(k) || /[\r\n\0]/.test(env[k])) throw Error('invalid environment field');
    save('state.json', JSON.stringify({ password, database: env.DATABASE_DBNAME || 'sub2api' }));
    save('node.env', Object.entries(env).map(([k, v]) => `${k}=${JSON.stringify(v)}`).join('\n') + '\n');
    const pricing = ssh('ali98', 'podman exec sub2api cat /app/data/model_pricing.json');
    JSON.parse(pricing);
    save('model_pricing.json', pricing);
    const known = execFileSync('ssh-keygen', ['-F', '139.224.110.98'], { stdio: ['ignore', 'pipe', 'pipe'] });
    if (!known.length) throw Error('missing trusted host key');
    save('known_hosts', known);
    console.log('Private environment, pricing snapshot and trusted host key prepared.');
  } else if (mode === 'role') {
    const { password, database } = JSON.parse(readFileSync(join(dir, 'state.json')));
    if (!/^[a-zA-Z0-9_]+$/.test(database) || !/^[a-f0-9]{64}$/.test(password)) throw Error('invalid private state');
    if (sql("SELECT count(*) FROM pg_roles WHERE rolname='lklb_bwg_node';").trim() !== '0') throw Error('role already exists; refusing replacement');
    sql(`BEGIN;
CREATE ROLE lklb_bwg_node LOGIN PASSWORD '${password}' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION CONNECTION LIMIT 8;
GRANT CONNECT ON DATABASE "${database}" TO lklb_bwg_node;
GRANT USAGE ON SCHEMA public TO lklb_bwg_node;
GRANT SELECT, INSERT, UPDATE ON ALL TABLES IN SCHEMA public TO lklb_bwg_node;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO lklb_bwg_node;
COMMIT;`);
    console.log('Dedicated database role created without DDL or DELETE grants.');
  } else if (mode === 'authorize') {
    const pub = ssh('bwg', 'cat /etc/lklb-bwg-node/tunnel-key.pub').trim();
    if (!/^ssh-ed25519 [A-Za-z0-9+/=]+(?: [^\r\n]*)?$/.test(pub)) throw Error('invalid public key');
    const prior = ssh('ali98', 'cat /home/admin/.ssh/authorized_keys');
    if (prior.includes(pub.split(' ')[1])) throw Error('tunnel key already installed');
    const line = `from="45.78.79.47",restrict,port-forwarding,permitopen="127.0.0.1:25432",permitopen="127.0.0.1:26379",command="/bin/false" ${pub}\n`;
    save('authorized_keys.before', prior);
    save('authorized_keys.next', prior + (prior.endsWith('\n') ? '' : '\n') + line);
    const digest = createHash('sha256').update(prior).digest('hex');
    execFileSync('scp', [join(dir, 'authorized_keys.next'), 'ali98:/home/admin/.ssh/authorized_keys.bwg-next'], { stdio: ['ignore', 'pipe', 'pipe'] });
    ssh('ali98', `test "$(sha256sum /home/admin/.ssh/authorized_keys | cut -d' ' -f1)" = '${digest}' && chmod 600 /home/admin/.ssh/authorized_keys.bwg-next && mv /home/admin/.ssh/authorized_keys.bwg-next /home/admin/.ssh/authorized_keys`);
    console.log('Restricted BWG tunnel public key appended; existing keys preserved.');
  } else if (mode === 'revoke') {
    sql('ALTER ROLE lklb_bwg_node NOLOGIN;');
    const pub = ssh('bwg', 'cat /etc/lklb-bwg-node/tunnel-key.pub').trim();
    if (!/^ssh-ed25519 [A-Za-z0-9+/=]+(?: [^\r\n]*)?$/.test(pub)) throw Error('invalid public key');
    const prior = ssh('ali98', 'cat /home/admin/.ssh/authorized_keys');
    const lines = prior.split('\n');
    const removed = lines.filter(line => line.includes(pub.split(' ')[1]));
    if (removed.length !== 1 || !removed[0].includes('lklb-bwg-state-tunnel')) throw Error('unexpected authorization state');
    save('authorized_keys.before-revoke', prior);
    save('authorized_keys.revoked', lines.filter(line => line !== removed[0]).join('\n'));
    const digest = createHash('sha256').update(prior).digest('hex');
    execFileSync('scp', [join(dir, 'authorized_keys.revoked'), 'ali98:/home/admin/.ssh/authorized_keys.bwg-revoke'], { stdio: ['ignore', 'pipe', 'pipe'] });
    ssh('ali98', `test "$(sha256sum /home/admin/.ssh/authorized_keys | cut -d' ' -f1)" = '${digest}' && chmod 600 /home/admin/.ssh/authorized_keys.bwg-revoke && mv /home/admin/.ssh/authorized_keys.bwg-revoke /home/admin/.ssh/authorized_keys`);
    console.log('Dedicated database login disabled and only the trial tunnel key removed.');
  } else if (mode === 'test-key') {
    const row = JSON.parse(sql(`SELECT row_to_json(k) FROM (SELECT id,key FROM api_keys WHERE user_id=1 AND status='active' AND deleted_at IS NULL AND group_id=2 AND quota=0 AND (expires_at IS NULL OR expires_at>NOW()) ORDER BY id LIMIT 1) k;`).trim());
    save('admin-test-key', row.key);
    console.log(`Admin test key selected: ID ${row.id}; credential not printed.`);
  } else { throw Error('unknown operation'); }
} catch {
  console.error('Preparation failed; no credential-bearing output has been printed. Inspect the private inputs or run a non-secret diagnostic.');
  process.exitCode = 1;
}
