import {readFileSync} from 'node:fs';
import {execFileSync} from 'node:child_process';
import {join} from 'node:path';
const dir = process.argv[2];
const ssh = (cmd, input) => execFileSync('ssh', ['ali98', cmd], {input, maxBuffer:32<<20, stdio:['pipe','pipe','pipe']}).toString();
try {
  const raw = ssh("podman exec lklb-edge-control find /app/edge-state/leases -name '*.json' -exec cat '{}' \\; -exec echo \\;");
  const rows = raw.trim().split('\n').filter(Boolean).map(line => JSON.parse(line));
  console.log(JSON.stringify({active:rows.filter(x => !['settled','uncertain'].includes(x.State)).map(x => ({id:x.ID,state:x.State,user:x.Snapshot?.Key?.UserID,created:x.CreatedAt}))}));
  const results = JSON.parse(readFileSync(join(dir,'concurrency-result.json'),'utf8'));
  const ids = results.map(x=>x.id).filter(Boolean);
  if (!ids.every(x=>/^[a-f0-9]{32}$/.test(x))) throw Error('invalid ID');
  const sql = `SELECT count(*) AS rows, count(DISTINCT request_id) AS unique_requests, sum(actual_cost) AS cost FROM usage_logs WHERE request_id IN (${ids.map(x=>"'edge:"+x+"'").join(',')});`;
  console.log(ssh(`podman exec -i sub2api-postgres sh -c 'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -P pager=off'`,sql));
} catch {
  console.error('Concurrency audit failed; private state withheld.');
  process.exitCode = 1;
}
