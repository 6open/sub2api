#!/usr/bin/env python3
import base64, hashlib, hmac, html, json, os, secrets, sqlite3, subprocess, sys, threading, time, traceback
from datetime import datetime, timezone
from http.server import HTTPServer, BaseHTTPRequestHandler
from socketserver import ThreadingMixIn
from pathlib import Path
from urllib.parse import parse_qs, urlencode, urlparse, quote
import urllib.request, urllib.error

BASE = Path(__file__).resolve().parent

def load_env():
    p = BASE / '.env'
    if p.exists():
        for line in p.read_text(encoding='utf-8').splitlines():
            line=line.strip()
            if line and not line.startswith('#') and '=' in line:
                k,v=line.split('=',1); os.environ.setdefault(k,v)
load_env()

DB_PATH=os.environ.get('DB_PATH','/opt/lklb-code-shop/data/shop.db')
ADMIN_TOKEN=os.environ.get('ADMIN_TOKEN','')
HOST=os.environ.get('HOST','127.0.0.1')
PORT=int(os.environ.get('PORT','19098'))
PUBLIC_BASE_URL=os.environ.get('PUBLIC_BASE_URL','https://lklb.top').rstrip('/')
SESSION_SECRET=os.environ.get('SESSION_SECRET') or ADMIN_TOKEN or 'change-me'

CONNECT_CLIENT_ID=os.environ.get('LINUXDO_CONNECT_CLIENT_ID','')
CONNECT_CLIENT_SECRET=os.environ.get('LINUXDO_CONNECT_CLIENT_SECRET','')
CONNECT_AUTHORIZE_URL='https://connect.linux.do/oauth2/authorize'
CONNECT_TOKEN_URL='https://connect.linux.do/oauth2/token'
CONNECT_USERINFO_URL='https://connect.linux.do/api/user'
CONNECT_REDIRECT_URI=PUBLIC_BASE_URL+'/api/linuxdo-connect/callback'

LDC_PID=os.environ.get('LINUXDO_CREDIT_PID') or os.environ.get('LDC_PID','')
LDC_KEY=os.environ.get('LINUXDO_CREDIT_KEY') or os.environ.get('LDC_KEY','')
LDC_SUBMIT_URL=os.environ.get('LINUXDO_CREDIT_SUBMIT_URL','https://credit.linux.do/epay/pay/submit.php')
LDC_QUERY_URL=os.environ.get('LINUXDO_CREDIT_QUERY_URL','').strip()
LDC_QUERY_INTERVAL_SECONDS=max(2,int(os.environ.get('LINUXDO_CREDIT_QUERY_INTERVAL_SECONDS','10')))
OUTBOUND_PROXY=os.environ.get('OUTBOUND_PROXY') or os.environ.get('HTTPS_PROXY') or os.environ.get('HTTP_PROXY') or ''
SUB2API_BASE_URL=os.environ.get('SUB2API_BASE_URL','http://127.0.0.1:18080/api/v1').rstrip('/')
CUSTOM_MIN_USD=int(os.environ.get('CUSTOM_MIN_USD','1'))
CUSTOM_MAX_USD=int(os.environ.get('CUSTOM_MAX_USD','100'))
REDEEM_CODE_PREFIX=os.environ.get('REDEEM_CODE_PREFIX','LDC-')

_credit_query_lock=threading.Lock()
_credit_query_last_attempt={}

PLANS={
    'usd1': {'label':'1 刀额度','usd_value':1,'redeem_value':1,'ldc':'40.00','title':'LKLB 中转站 1刀额度'},
    'usd5': {'label':'5 刀额度','usd_value':5,'redeem_value':5,'ldc':'200.00','title':'LKLB 中转站 5刀额度'},
    'usd10': {'label':'10 刀额度','usd_value':10,'redeem_value':10,'ldc':'400.00','title':'LKLB 中转站 10刀额度'},
}

PROMO_USD_LIMIT=10
PROMO_LDC_PER_USD=10
NORMAL_LDC_PER_USD=40

def remaining_promo_usd(issued_usd_value):
    try:
        used=float(issued_usd_value or 0)
    except Exception:
        used=0
    return max(0, PROMO_USD_LIMIT-used)

def calculate_ldc_amount(usd_value, issued_usd_value):
    usd=float(usd_value or 0)
    promo=min(usd, remaining_promo_usd(issued_usd_value))
    normal=max(0, usd-promo)
    return f'{promo*PROMO_LDC_PER_USD + normal*NORMAL_LDC_PER_USD:.2f}'


def calculate_usd_from_ldc(ldc_amount, issued_usd_value):
    ldc=float(ldc_amount or 0)
    promo_usd=remaining_promo_usd(issued_usd_value)
    promo_ldc=promo_usd*PROMO_LDC_PER_USD
    promo_part_ldc=min(ldc, promo_ldc)
    normal_part_ldc=max(0, ldc-promo_part_ldc)
    usd=promo_part_ldc/PROMO_LDC_PER_USD + normal_part_ldc/NORMAL_LDC_PER_USD
    return round(usd, 8)


def parse_ldc_amount(params):
    raw=first(params,['ldc','amount','value']).strip()
    if not raw:
        raise ValueError('请输入 LDC 数量')
    try:
        value=float(raw)
    except Exception:
        raise ValueError('请输入有效的 LDC 数量')
    if value <= 0:
        raise ValueError('LDC 数量必须大于 0')
    if value > 5000:
        raise ValueError('单次最多 5000 LDC')
    # 支付接口金额保留 2 位，避免过多小数
    return round(value, 2)

def plan_display_ldc(plan, issued_usd_value=0):
    return calculate_ldc_amount(PLANS[plan]['usd_value'], issued_usd_value)


def local_ldc_reserved_usd(c, linuxdo_user_sub):
    """USD already occupying the LinuxDO Credit promo bucket in code-shop.

    Only successful local orders should consume the promo bucket. Pending,
    expired, or failed orders may never be paid and must not reduce the
    discounted quota for later successful payments.
    """
    if not linuxdo_user_sub:
        return 0
    row = c.execute(
        "SELECT COALESCE(SUM(usd_value),0) n FROM purchase_orders WHERE user_sub=? AND status IN ('issued','credited')",
        (str(linuxdo_user_sub),),
    ).fetchone()
    try:
        return float(row['n'] or 0)
    except Exception:
        return 0


def sub2api_external_ldc_redeemed_usd(linuxdo_user_sub, linuxdo_username):
    """USD already redeemed through legacy/direct LDC codes in sub2api.

    New code-shop orders are counted from local purchase_orders, so this query
    intentionally only counts notes.code_kind=ldc direct redeem codes and avoids
    double-counting notes.source=linuxdo_credit_shop rows generated by code-shop.
    """
    sub = str(linuxdo_user_sub or '').strip()
    username = str(linuxdo_username or '').strip()
    if not sub and not username:
        return 0
    sql = """
WITH matched_users AS (
    SELECT id FROM users WHERE :'username' <> '' AND username = :'username'
    UNION
    SELECT user_id FROM auth_identities
     WHERE :'sub' <> ''
       AND provider_subject = :'sub'
       AND (provider_type ILIKE '%linux%' OR provider_key ILIKE '%linux%')
), json_redeems AS (
    SELECT rc.*, rc.notes::jsonb AS j
      FROM redeem_codes rc
     WHERE rc.status='used'
       AND rc.type='balance'
       AND rc.used_by IN (SELECT id FROM matched_users)
       AND rc.notes IS NOT NULL
       AND btrim(rc.notes) LIKE '{%'
)
SELECT COALESCE(SUM(
    CASE
      WHEN (j->>'credited_usd') ~ '^[0-9]+(\\.[0-9]+)?$'
        THEN (j->>'credited_usd')::numeric
      ELSE 0
    END
),0)
FROM json_redeems
WHERE j->>'code_kind' = 'ldc';
"""
    cmd = [
        'podman','exec','-i','sub2api-postgres','psql',
        '-U','sub2api','-d','sub2api',
        '-v','sub='+sub,
        '-v','username='+username,
        '-qAt',
    ]
    try:
        proc = subprocess.run(cmd, input=sql, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10)
        if proc.returncode != 0:
            sys.stderr.write('[ldc-promo] sub2api query failed: '+proc.stderr.strip()+'\n')
            return 0
        out = (proc.stdout or '').strip().splitlines()
        if not out:
            return 0
        return float(out[-1] or '0')
    except Exception as e:
        sys.stderr.write('[ldc-promo] sub2api query exception: '+repr(e)+'\n')
        return 0


def unified_ldc_used_usd(c, user):
    """Total USD that has consumed the first-10-USD LinuxDO Credit promo bucket."""
    if not user:
        return 0
    local = local_ldc_reserved_usd(c, user.get('sub'))
    external = sub2api_external_ldc_redeemed_usd(user.get('sub'), user.get('username'))
    return local + external


def parse_custom_usd(params):
    raw=first(params,['amount','usd','value']).strip()
    if not raw or not raw.isdigit():
        raise ValueError('请输入整数额度')
    value=int(raw)
    if value < CUSTOM_MIN_USD or value > CUSTOM_MAX_USD:
        raise ValueError(f'额度范围为 {CUSTOM_MIN_USD}-{CUSTOM_MAX_USD} 刀')
    return value

def make_redeem_code(out_trade_no):
    digest=hashlib.sha256(str(out_trade_no).encode('utf-8')).hexdigest().upper()
    return (REDEEM_CODE_PREFIX + digest)[:32]


def normalize_number(value):
    n=float(value or 0)
    if n.is_integer():
        return int(n)
    return round(n, 8)


def build_sub2api_redeem_code_payload(out_trade_no, ldc_amount, usd_value, linuxdo_user_sub, linuxdo_username):
    code=make_redeem_code(out_trade_no)
    ldc_value=f'{float(ldc_amount):.2f}'
    notes={
        'code_kind':'ldc',
        'source':'linuxdo_credit_shop',
        'out_trade_no':out_trade_no,
        'linuxdo_user_sub':linuxdo_user_sub,
        'linuxdo_username':linuxdo_username,
        'ldc_amount':normalize_number(ldc_amount),
        'usd_value':normalize_number(usd_value),
    }
    return code, ldc_value, notes


def create_sub2api_redeem_code(out_trade_no, ldc_amount, usd_value, linuxdo_user_sub, linuxdo_username):
    code, value, notes_obj = build_sub2api_redeem_code_payload(out_trade_no, ldc_amount, usd_value, linuxdo_user_sub, linuxdo_username)
    notes=json.dumps(notes_obj, ensure_ascii=False, separators=(',',':'))
    sql="INSERT INTO redeem_codes(code,type,value,status,notes,created_at,validity_days) VALUES (:'code','balance',:'value','unused',:'notes',NOW(),30) ON CONFLICT (code) DO UPDATE SET value=EXCLUDED.value, notes=EXCLUDED.notes RETURNING code;"
    cmd=['podman','exec','-i','sub2api-postgres','psql','-U','sub2api','-d','sub2api','-v','code='+code,'-v','value='+str(value),'-v','notes='+notes,'-qAt']
    proc=subprocess.run(cmd, input=sql, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=15)
    if proc.returncode != 0:
        raise RuntimeError('create sub2api redeem code failed: '+proc.stderr.strip())
    return code


def created_order_processing_html(order_id):
    oid=html.escape(str(order_id or ''), quote=True)
    return """
<div class='ok' id='payment-processing'>支付处理中，请稍候。系统正在等待 LDC 支付通知，通常几秒内会自动刷新出兑换码。</div>
<script>
(function(){
  var orderId = '%s';
  var attempts = 0;
  var maxAttempts = 60;
  function poll(){
    attempts += 1;
    fetch('/api/order?order_id=' + encodeURIComponent(orderId), {cache: 'no-store'})
      .then(function(r){ return r.ok ? r.json() : null; })
      .then(function(data){
        var order = data && data.order;
        if (!order) return;
        if (order.status && order.status !== 'created') {
          window.location.reload();
          return;
        }
      })
      .catch(function(){})
      .finally(function(){
        if (attempts < maxAttempts) window.setTimeout(poll, 2000);
      });
  }
  window.setTimeout(poll, 1200);
})();
</script>
""" % oid

def now_iso(): return datetime.now(timezone.utc).isoformat()

def conn():
    Path(DB_PATH).parent.mkdir(parents=True, exist_ok=True)
    c=sqlite3.connect(DB_PATH,timeout=30)
    c.row_factory=sqlite3.Row
    c.execute('PRAGMA journal_mode=WAL')
    c.execute('PRAGMA busy_timeout=30000')
    return c

def table_cols(c, table):
    return {r[1] for r in c.execute(f'PRAGMA table_info({table})')}

def init_db():
    with conn() as c:
        c.executescript('''
CREATE TABLE IF NOT EXISTS codes(id INTEGER PRIMARY KEY AUTOINCREMENT, code TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'available', batch TEXT, imported_at TEXT NOT NULL, issued_at TEXT, order_key TEXT);
CREATE INDEX IF NOT EXISTS idx_codes_status_id ON codes(status,id);
CREATE TABLE IF NOT EXISTS orders(id INTEGER PRIMARY KEY AUTOINCREMENT, order_key TEXT NOT NULL UNIQUE, linuxdo_order_id TEXT, status TEXT NOT NULL, code TEXT, raw_params TEXT NOT NULL, request_method TEXT NOT NULL, remote_addr TEXT, user_agent TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_orders_created ON orders(created_at);
CREATE TABLE IF NOT EXISTS purchase_orders(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  out_trade_no TEXT NOT NULL UNIQUE,
  plan TEXT NOT NULL,
  ldc_amount TEXT NOT NULL,
  usd_value INTEGER NOT NULL,
  user_sub TEXT NOT NULL,
  username TEXT,
  status TEXT NOT NULL,
  credit_trade_no TEXT,
  credit_pay_url TEXT,
  code TEXT,
  raw_notify TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_purchase_user_plan ON purchase_orders(user_sub,plan,status);
CREATE INDEX IF NOT EXISTS idx_purchase_created ON purchase_orders(created_at);
''')
        cols=table_cols(c,'orders')
        if 'buyer_key' not in cols:
            c.execute('ALTER TABLE orders ADD COLUMN buyer_key TEXT')
        if 'buyer_label' not in cols:
            c.execute('ALTER TABLE orders ADD COLUMN buyer_label TEXT')
        c.execute('CREATE INDEX IF NOT EXISTS idx_orders_buyer_key ON orders(buyer_key)')
        c.execute("CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_buyer_key_issued ON orders(buyer_key) WHERE buyer_key IS NOT NULL AND buyer_key != '' AND status='issued'")
        cols=table_cols(c,'purchase_orders')
        if 'sub2api_user_id' not in cols:
            c.execute('ALTER TABLE purchase_orders ADD COLUMN sub2api_user_id INTEGER')
        if 'sub2api_user_email' not in cols:
            c.execute('ALTER TABLE purchase_orders ADD COLUMN sub2api_user_email TEXT')
        if 'delivery_message' not in cols:
            c.execute('ALTER TABLE purchase_orders ADD COLUMN delivery_message TEXT')
        cols=table_cols(c,'codes')
        if 'plan' not in cols:
            c.execute("ALTER TABLE codes ADD COLUMN plan TEXT NOT NULL DEFAULT 'usd1'")
        c.execute('CREATE INDEX IF NOT EXISTS idx_codes_plan_status_id ON codes(plan,status,id)')

def flatten(qs):
    return {k:(v if len(v)>1 else v[0]) for k,v in qs.items()}

def first(params,names):
    for n in names:
        v=params.get(n)
        if isinstance(v,list): v=v[0] if v else ''
        if v is not None and str(v).strip(): return str(v).strip()
    return ''

def b64url(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).decode().rstrip('=')

def b64url_decode(s: str) -> bytes:
    return base64.urlsafe_b64decode(s + '='*((4-len(s)%4)%4))

def sign_session(data):
    payload=dict(data)
    payload.setdefault('iat', int(time.time()))
    raw=json.dumps(payload, ensure_ascii=False, separators=(',',':'), sort_keys=True).encode()
    p=b64url(raw)
    sig=hmac.new(SESSION_SECRET.encode(), p.encode(), hashlib.sha256).hexdigest()
    return p+'.'+sig

def verify_session(token):
    if not token or '.' not in token: return None
    p,sig=token.rsplit('.',1)
    good=hmac.new(SESSION_SECRET.encode(), p.encode(), hashlib.sha256).hexdigest()
    if not hmac.compare_digest(sig, good): return None
    try: data=json.loads(b64url_decode(p).decode('utf-8'))
    except Exception: return None
    if int(data.get('iat',0)) < int(time.time()) - 86400*30: return None
    return data

def get_cookie(headers, name):
    raw=headers.get('Cookie') or ''
    for part in raw.split(';'):
        if '=' in part:
            k,v=part.strip().split('=',1)
            if k==name: return v
    return ''

def decoded_cookie(headers, name):
    value=get_cookie(headers, name)
    if not value: return ''
    try: return b64url_decode(value).decode('utf-8')
    except Exception: return ''

def cookie(name, value, max_age=2592000):
    return f'{name}={value}; Path=/; Max-Age={max_age}; HttpOnly; Secure; SameSite=Lax'

def clear_cookie(name):
    return f'{name}=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=Lax'

def html_page(title, body):
    return '''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{}</title><style>

:root{{--bg:#f5f7fb;--card:#fff;--text:#0f172a;--muted:#64748b;--muted2:#94a3b8;--line:#e2e8f0;--brand:#4f46e5;--brand2:#7c3aed;--brand3:#06b6d4;--soft:#eef2ff;--soft2:#f8fafc;--ok:#16a34a;--warn:#f59e0b}}*{{box-sizing:border-box}}body{{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",Arial,"Noto Sans SC",sans-serif;background:radial-gradient(circle at 18% 0,rgba(124,58,237,.16),transparent 30%),radial-gradient(circle at 82% 8%,rgba(6,182,212,.14),transparent 28%),linear-gradient(180deg,#f8fafc,#eef2f7);margin:0;color:var(--text)}}.wrap{{max-width:1120px;margin:32px auto;padding:18px}}.card{{position:relative;overflow:hidden;background:rgba(255,255,255,.94);border:1px solid rgba(226,232,240,.9);border-radius:30px;padding:38px;box-shadow:0 28px 80px rgba(15,23,42,.12)}}.card:before{{content:"";position:absolute;inset:0 0 auto 0;height:5px;background:linear-gradient(90deg,var(--brand),var(--brand2),var(--brand3));}}.hero{{position:relative;display:grid;grid-template-columns:1fr auto;align-items:start;gap:18px;margin-bottom:22px}}h1{{margin:0;font-size:42px;letter-spacing:-.045em;line-height:1.08}}.subtitle{{max-width:760px;margin:14px 0 0;color:var(--muted);font-size:16px;line-height:1.85}}.muted{{color:var(--muted);line-height:1.65}}.login-note{{display:inline-flex;align-items:center;gap:8px;margin-top:16px;border-radius:999px;background:#fff;border:1px solid var(--line);padding:9px 13px;color:#475569;font-size:14px;box-shadow:0 8px 20px rgba(15,23,42,.04)}}.btn,.btn2,button{{display:inline-flex;align-items:center;justify-content:center;border:0;text-decoration:none;font-size:15px;font-weight:750;cursor:pointer;transition:transform .15s ease,box-shadow .15s ease,opacity .15s ease}}.btn:hover,button:hover{{transform:translateY(-1px)}}.btn2{{background:#0f172a;color:#fff;padding:9px 13px;border-radius:999px;box-shadow:0 10px 22px rgba(15,23,42,.16)}}button{{width:100%;min-height:46px;background:linear-gradient(135deg,var(--brand),var(--brand2));color:#fff;padding:12px 16px;border-radius:15px;box-shadow:0 14px 28px rgba(79,70,229,.24)}}button:active{{transform:translateY(0);opacity:.9}}.rule-grid{{display:none;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px;margin:18px 0 24px}}.rule{{border:1px solid var(--line);background:linear-gradient(180deg,#fff,#f8fafc);border-radius:18px;padding:14px 15px}}.rule b{{display:block;font-size:14px;margin-bottom:6px}}.rule span{{display:block;color:var(--muted);font-size:13px;line-height:1.55}}.plans{{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px;margin-top:6px}}.plan{{position:relative;min-width:0;display:flex;flex-direction:column;gap:14px;border:1px solid var(--line);border-radius:24px;padding:22px;background:linear-gradient(180deg,#fff,#fbfdff);box-shadow:0 12px 34px rgba(15,23,42,.055)}}.plan:hover{{border-color:#c7d2fe;box-shadow:0 18px 44px rgba(79,70,229,.10)}}.plan-head{{display:flex;align-items:flex-start;justify-content:space-between;gap:10px}}.plan h3{{margin:0;font-size:21px;line-height:1.25}}.badge{{flex:none;border:1px solid #ddd6fe;background:#f5f3ff;color:#5b21b6;border-radius:999px;padding:5px 9px;font-size:12px;font-weight:750}}.price{{margin:0;font-size:34px;font-weight:900;letter-spacing:-.045em;line-height:1.05;white-space:nowrap}}.price .unit{{font-size:17px;letter-spacing:0;margin-left:5px}}.login-price{{font-size:28px;color:#334155;letter-spacing:-.03em}}.meta{{display:grid;gap:7px;margin-top:-2px}}.meta-row{{display:flex;align-items:center;justify-content:space-between;gap:10px;color:#64748b;font-size:13px}}.meta-row strong{{color:#0f172a;font-weight:750}}.normal{{margin:0;color:#64748b;font-size:14px;line-height:1.6}}.hint{{margin:0;color:#64748b;font-size:13px;line-height:1.55}}.plan form{{margin-top:auto;display:flex;gap:10px;align-items:center}}.custom{{grid-column:1/-1;display:grid;grid-template-columns:1fr 1.25fr;align-items:center;gap:18px}}.custom .price{{font-size:28px}}.custom form{{margin-top:0;display:grid;grid-template-columns:1fr 210px;gap:12px}}.custom-copy{{display:grid;gap:8px}}.custom-copy h3{{font-size:22px}}.custom-copy .price{{color:#111827}}input{{width:100%;min-width:0;min-height:46px;padding:12px 14px;border:1px solid #d9dee8;border-radius:15px;font-size:15px;outline:none;background:#fff}}input:focus{{border-color:var(--brand);box-shadow:0 0 0 4px rgba(79,70,229,.10)}}.foot{{margin:22px 0 0;color:#64748b;font-size:14px;line-height:1.7}}.foot b{{color:#0f172a}}.code{{font-size:22px;font-weight:800;background:#f0f4ff;border:1px solid #d6e2ff;border-radius:12px;padding:18px;word-break:break-all}}.err{{background:#fff3f0;border:1px solid #ffd2c8;color:#9f2a13;border-radius:12px;padding:14px;margin:12px 0}}.ok{{background:#effaf2;border:1px solid #bfe8c8;color:#176529;border-radius:12px;padding:14px;margin:12px 0}}@media(max-width:920px){{.hero{{grid-template-columns:1fr}}.rule-grid{{grid-template-columns:1fr}}.plans{{grid-template-columns:1fr 1fr}}.custom{{grid-template-columns:1fr}}.custom form{{grid-template-columns:1fr}}h1{{font-size:36px}}}}@media(max-width:600px){{.wrap{{margin:12px auto;padding:10px}}.card{{padding:24px;border-radius:24px}}.plans{{grid-template-columns:1fr}}.price{{font-size:32px}}.plan{{padding:20px}}.plan-head{{display:block}}.badge{{display:inline-flex;margin-top:8px}}.login-note{{border-radius:16px;align-items:flex-start}}.btn2{{margin-top:8px}}.custom form{{grid-template-columns:1fr}}}}
</style></head><body><div class="wrap"><div class="card">{} </div></div></body></html>'''.format(html.escape(title), body)

def json_bytes(obj,code=200):
    return code, [('Content-Type','application/json; charset=utf-8')], json.dumps(obj,ensure_ascii=False).encode()

def text_bytes(s,code=200,ct='text/plain; charset=utf-8',headers=None):
    hs=[('Content-Type',ct)]
    if headers: hs.extend(headers)
    return code, hs, s.encode()

def redirect_bytes(url, headers=None):
    hs=[('Location',url)]
    if headers: hs.extend(headers)
    return 302, hs, b''

def epay_sign(params, secret):
    pairs=[]
    for k in sorted(params):
        if k in ('sign','sign_type'): continue
        v=params[k]
        if v is None or str(v)=='': continue
        pairs.append(f'{k}={v}')
    return hashlib.md5(('&'.join(pairs)+secret).encode('utf-8')).hexdigest()

def verify_epay_notify(params):
    if not LDC_KEY: return False
    got=first(params,['sign'])
    if not got: return False
    return hmac.compare_digest(got.lower(), epay_sign(params,LDC_KEY).lower())

def opener():
    handlers=[]
    if OUTBOUND_PROXY:
        handlers.append(urllib.request.ProxyHandler({'http':OUTBOUND_PROXY,'https':OUTBOUND_PROXY}))
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl): return None
    handlers.append(NoRedirect)
    return urllib.request.build_opener(*handlers)

def http_post_form(url, data, headers=None, timeout=20):
    body=urlencode(data).encode('utf-8')
    req=urllib.request.Request(url, data=body, method='POST', headers={'Content-Type':'application/x-www-form-urlencoded', 'User-Agent':'lklb-code-shop/1.1', **(headers or {})})
    op=opener()
    try:
        r=op.open(req, timeout=timeout)
        return r.status, dict(r.headers), r.read().decode('utf-8','replace')
    except urllib.error.HTTPError as e:
        return e.code, dict(e.headers), e.read().decode('utf-8','replace')

def http_get_json(url, headers=None, timeout=20):
    req=urllib.request.Request(url, headers={'User-Agent':'lklb-code-shop/1.1', **(headers or {})})
    r=opener().open(req, timeout=timeout)
    return json.loads(r.read().decode('utf-8'))

def credit_query_endpoint():
    if LDC_QUERY_URL:
        return LDC_QUERY_URL
    parsed=urlparse(LDC_SUBMIT_URL)
    if not parsed.scheme or not parsed.netloc or '/pay/' not in parsed.path:
        raise RuntimeError('LinuxDO Credit query URL is not configured')
    prefix=parsed.path.split('/pay/',1)[0]
    return '{}://{}{}{}'.format(parsed.scheme,parsed.netloc,prefix,'/api.php')

def query_credit_order(out_trade_no):
    if not LDC_PID or not LDC_KEY:
        raise RuntimeError('LinuxDO Credit is not configured')
    params={
        'act':'order',
        'pid':LDC_PID,
        'key':LDC_KEY,
        'out_trade_no':str(out_trade_no),
    }
    data=http_get_json(credit_query_endpoint()+'?'+urlencode(params), timeout=10)
    if not isinstance(data,dict) or str(data.get('code','')) != '1':
        msg=data.get('msg') if isinstance(data,dict) else 'invalid response'
        raise RuntimeError('Credit query failed: '+str(msg or 'unknown error'))
    if str(data.get('out_trade_no') or '') != str(out_trade_no):
        raise RuntimeError('Credit query returned a different order')
    if str(data.get('pid') or '') != str(LDC_PID):
        raise RuntimeError('Credit query returned a different merchant')
    return data

def claim_credit_query(out_trade_no, force=False):
    now=time.monotonic()
    with _credit_query_lock:
        last=_credit_query_last_attempt.get(str(out_trade_no),0)
        if not force and now-last < LDC_QUERY_INTERVAL_SECONDS:
            return False
        _credit_query_last_attempt[str(out_trade_no)]=now
        if len(_credit_query_last_attempt) > 2048:
            cutoff=now-3600
            for key,checked_at in list(_credit_query_last_attempt.items()):
                if checked_at < cutoff:
                    _credit_query_last_attempt.pop(key,None)
        return True

def http_post_json_direct(url, obj, headers=None, timeout=10):
    body=json.dumps(obj, ensure_ascii=False).encode('utf-8')
    req=urllib.request.Request(url, data=body, method='POST', headers={'Content-Type':'application/json', 'User-Agent':'lklb-code-shop/1.1', **(headers or {})})
    # Local sub2api calls must not go through the outbound proxy.
    op=urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        r=op.open(req, timeout=timeout)
        return r.status, dict(r.headers), r.read().decode('utf-8','replace')
    except urllib.error.HTTPError as e:
        return e.code, dict(e.headers), e.read().decode('utf-8','replace')

def verify_sub2api_handoff(token):
    token=str(token or '').strip()
    if not token:
        return None
    status, headers, body = http_post_json_direct(SUB2API_BASE_URL + '/auth/linuxdo-shop/handoff/verify', {'token': token}, timeout=8)
    if status >= 400:
        raise RuntimeError('verify sub2api handoff failed: HTTP %s %s' % (status, body[:300]))
    data=json.loads(body or '{}')
    if isinstance(data, dict) and data.get('code') == 0:
        data=data.get('data') or {}
    user_id=int(data.get('user_id') or 0)
    if user_id <= 0:
        raise RuntimeError('verify sub2api handoff returned no user_id')
    return {'user_id': user_id, 'email': str(data.get('email') or '')}

def handoff_cookie(headers):
    return verify_session(get_cookie(headers,'lklb_handoff'))

def handoff_set_cookie(handoff):
    return cookie('lklb_handoff', sign_session(handoff), 600)

def merge_handoff_into_user(user, handoff):
    merged=dict(user or {})
    if handoff and int(handoff.get('user_id') or 0) > 0:
        merged['sub2api_user_id']=int(handoff.get('user_id'))
        merged['sub2api_user_email']=str(handoff.get('email') or '')
    return merged

def psql_exec(sql, vars=None, timeout=15):
    cmd=['podman','exec','-i','sub2api-postgres','psql','-U','sub2api','-d','sub2api','-qAt']
    for k,v in (vars or {}).items():
        cmd.extend(['-v', str(k)+'='+str(v)])
    proc=subprocess.run(cmd, input=sql, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or 'psql failed')
    return (proc.stdout or '').strip()

def ensure_sub2api_linuxdo_binding(sub2api_user_id, linuxdo_user_sub, linuxdo_username):
    uid=int(sub2api_user_id or 0)
    sub=str(linuxdo_user_sub or '').strip()
    username=str(linuxdo_username or '').strip()
    if uid <= 0 or not sub:
        return False
    check_sql="""
SELECT CASE
  WHEN EXISTS (SELECT 1 FROM auth_identities WHERE provider_type='linuxdo' AND provider_key='linuxdo' AND provider_subject=:'sub' AND user_id <> :'uid'::bigint) THEN 'subject_conflict'
  WHEN EXISTS (SELECT 1 FROM auth_identities WHERE provider_type='linuxdo' AND provider_key='linuxdo' AND user_id=:'uid'::bigint AND provider_subject <> :'sub') THEN 'user_conflict'
  WHEN NOT EXISTS (SELECT 1 FROM users WHERE id=:'uid'::bigint) THEN 'user_missing'
  ELSE 'ok'
END;
"""
    status=psql_exec(check_sql, {'uid':uid,'sub':sub}, timeout=10).splitlines()[-1].strip()
    if status != 'ok':
        raise RuntimeError('linuxdo binding conflict: '+status)
    notes=json.dumps({'source':'linuxdo_credit_shop','username':username}, ensure_ascii=False, separators=(',',':'))
    sql="""
INSERT INTO auth_identities(user_id,provider_type,provider_key,provider_subject,verified_at,metadata,created_at,updated_at)
VALUES (:'uid'::bigint,'linuxdo','linuxdo',:'sub',NOW(),:'metadata'::jsonb,NOW(),NOW())
ON CONFLICT (provider_type,provider_key,provider_subject) DO UPDATE
SET verified_at=NOW(), metadata=EXCLUDED.metadata, updated_at=NOW()
WHERE auth_identities.user_id=EXCLUDED.user_id;
"""
    psql_exec(sql, {'uid':uid,'sub':sub,'metadata':notes}, timeout=10)
    return True

def make_auto_credit_code(out_trade_no):
    digest=hashlib.sha256(('auto:'+str(out_trade_no)).encode('utf-8')).hexdigest().upper()
    return ('LDC-AUTO-' + digest)[:32]

def credit_sub2api_user_direct(out_trade_no, sub2api_user_id, credited_usd, ldc_amount, linuxdo_user_sub, linuxdo_username):
    uid=int(sub2api_user_id or 0)
    usd=float(credited_usd or 0)
    if uid <= 0 or usd <= 0:
        return False
    code=make_auto_credit_code(out_trade_no)
    notes=json.dumps({
        'code_kind':'ldc',
        'source':'linuxdo_credit_shop_auto_credit',
        'out_trade_no':out_trade_no,
        'linuxdo_user_sub':linuxdo_user_sub,
        'linuxdo_username':linuxdo_username,
        'ldc_amount':normalize_number(ldc_amount),
        'usd_value':normalize_number(credited_usd),
        'credited_usd':normalize_number(credited_usd),
        'redeemed_user_id':uid,
    }, ensure_ascii=False, separators=(',',':'))
    sql="""
WITH ins AS (
  INSERT INTO redeem_codes(code,type,value,status,used_by,used_at,notes,created_at,validity_days)
  VALUES (:'code','admin_balance',:'usd'::numeric,'used',:'uid'::bigint,NOW(),:'notes',NOW(),30)
  ON CONFLICT (code) DO NOTHING
  RETURNING 1
), upd AS (
  UPDATE users SET balance=balance+:'usd'::numeric, updated_at=NOW()
   WHERE id=:'uid'::bigint AND EXISTS (SELECT 1 FROM ins)
  RETURNING 1
)
SELECT COALESCE((SELECT COUNT(*) FROM ins),0);
"""
    out=psql_exec(sql, {'code':code,'uid':uid,'usd':usd,'notes':notes}, timeout=15)
    lines=[x.strip() for x in out.splitlines() if x.strip()]
    return bool(lines and lines[-1] in ('1','0'))

def exchange_connect_code(code_value):
    data={'grant_type':'authorization_code','code':code_value,'redirect_uri':CONNECT_REDIRECT_URI,'client_id':CONNECT_CLIENT_ID,'client_secret':CONNECT_CLIENT_SECRET}
    status, headers, body=http_post_form(CONNECT_TOKEN_URL, data)
    if status>=400: raise RuntimeError(f'token exchange failed: HTTP {status} {body[:300]}')
    token=json.loads(body)
    access=token.get('access_token')
    if not access: raise RuntimeError('token exchange no access_token')
    return http_get_json(CONNECT_USERINFO_URL, headers={'Authorization':'Bearer '+access})

def make_order_no(plan):
    return 'LKLB'+datetime.now().strftime('%Y%m%d%H%M%S')+plan[-1]+secrets.token_hex(4).upper()

def create_credit_payment(out_trade_no, plan, ldc_amount=None, title=None):
    p=PLANS.get(plan, {'title': title or 'LKLB 中转站额度', 'ldc': ldc_amount or '0.00'})
    params={
        'pid':LDC_PID,
        'type':'epay',
        'out_trade_no':out_trade_no,
        'name':p['title'],
        'money':ldc_amount or p['ldc'],
        'notify_url':PUBLIC_BASE_URL+'/api/linuxdo-credit/notify',
        'return_url':PUBLIC_BASE_URL+'/buy/result?order_id='+quote(out_trade_no),
    }
    params['sign']=epay_sign(params,LDC_KEY)
    params['sign_type']='MD5'
    status, headers, body=http_post_form(LDC_SUBMIT_URL, params)
    loc=headers.get('Location') or headers.get('location')
    if loc: return loc
    try: data=json.loads(body)
    except Exception: data={}
    loc=None
    if isinstance(data,dict):
        loc=data.get('url') or data.get('pay_url')
        order_no=data.get('order_no')
        d=data.get('data')
        if isinstance(d,dict):
            if not loc: loc=d.get('url') or d.get('pay_url')
            if not order_no: order_no=d.get('order_no')
        if loc: return loc
        if order_no: return 'https://credit.linux.do/paying?order_no='+quote(str(order_no))
        msg=data.get('error_msg') or data.get('msg') or body[:500]
    else: msg=body[:500]
    raise RuntimeError(f'Credit 创建订单失败 HTTP {status}: {msg}')

def derive_order_key(params):
    oid=first(params,['out_trade_no','trade_no','order_no','order_id','orderId','transaction_id','payment_id','pay_id','no','id'])
    if oid: return oid, oid
    canonical=json.dumps(params,sort_keys=True,ensure_ascii=False,separators=(',',':'))
    return 'sha256:'+hashlib.sha256(canonical.encode()).hexdigest(), ''

def derive_buyer_key(params):
    candidates=['buyer_id','buyerId','buyer_uid','buyerUid','buyer_user_id','buyerUserId','user_id','userId','uid','linuxdo_user_id','linuxdoUserId','linuxdo_uid','member_id','memberId','customer_id','customerId','payer_id','payerId','username','user_name','buyer_name','buyerName','linuxdo_username']
    for name in candidates:
        v=first(params,[name])
        if v: return name+':'+v, v
    return '', ''

def issue_legacy(order_key, linuxdo_order_id, params, method, headers, client):
    raw=json.dumps(params,ensure_ascii=False,sort_keys=True)
    remote=(headers.get('X-Forwarded-For') or client or '').split(',')[0].strip()
    ua=(headers.get('User-Agent') or '')[:500]
    buyer_key,buyer_label=derive_buyer_key(params)
    t=now_iso()
    with conn() as c:
        c.execute('BEGIN IMMEDIATE')
        row=c.execute('SELECT * FROM orders WHERE order_key=?',(order_key,)).fetchone()
        if row:
            c.commit(); return dict(row), False
        if buyer_key:
            prev=c.execute("SELECT * FROM orders WHERE buyer_key=? AND status='issued' ORDER BY id LIMIT 1",(buyer_key,)).fetchone()
            if prev:
                c.execute('INSERT INTO orders(order_key,linuxdo_order_id,status,code,raw_params,request_method,remote_addr,user_agent,created_at,updated_at,buyer_key,buyer_label) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)',(order_key,linuxdo_order_id,'duplicate_buyer',None,raw,method,remote,ua,t,t,buyer_key,buyer_label))
                row=c.execute('SELECT * FROM orders WHERE order_key=?',(order_key,)).fetchone(); c.commit(); return dict(row), True
        code_row=c.execute("SELECT id,code FROM codes WHERE status='available' AND plan='usd1' ORDER BY id LIMIT 1").fetchone()
        if code_row:
            code=code_row['code']; status='issued'
            c.execute("UPDATE codes SET status='issued',issued_at=?,order_key=? WHERE id=?",(t,order_key,code_row['id']))
        else:
            code=None; status='pending_no_stock'
        c.execute('INSERT INTO orders(order_key,linuxdo_order_id,status,code,raw_params,request_method,remote_addr,user_agent,created_at,updated_at,buyer_key,buyer_label) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)',(order_key,linuxdo_order_id,status,code,raw,method,remote,ua,t,t,buyer_key,buyer_label))
        row=c.execute('SELECT * FROM orders WHERE order_key=?',(order_key,)).fetchone(); c.commit(); return dict(row), True

def handle_credit_notify(params, method, headers, client):
    out_trade_no=first(params,['out_trade_no'])
    raw=json.dumps(params,ensure_ascii=False,sort_keys=True)
    if out_trade_no:
        with conn() as c:
            po=c.execute('SELECT * FROM purchase_orders WHERE out_trade_no=?',(out_trade_no,)).fetchone()
        if po:
            if not verify_epay_notify(params): return 'fail'
            if first(params,['trade_status']) != 'TRADE_SUCCESS': return 'success'
            if first(params,['pid']) and first(params,['pid']) != LDC_PID: return 'fail'
            plan=po['plan']; expected=po['ldc_amount']; paid=first(params,['money'])
            if paid and float(paid) != float(expected): return 'fail'
            t=now_iso()
            with conn() as c:
                c.execute('BEGIN IMMEDIATE')
                po=c.execute('SELECT * FROM purchase_orders WHERE out_trade_no=?',(out_trade_no,)).fetchone()
                if po['status'] in ('issued','credited'): c.commit(); return 'success'
                try:
                    if int(po['sub2api_user_id'] or 0) > 0:
                        ensure_sub2api_linuxdo_binding(po['sub2api_user_id'], po['user_sub'], po['username'] or '')
                        credit_sub2api_user_direct(out_trade_no, po['sub2api_user_id'], po['usd_value'], po['ldc_amount'], po['user_sub'], po['username'] or '')
                        c.execute("UPDATE purchase_orders SET status='credited', code=NULL, raw_notify=?, credit_trade_no=?, delivery_message=?, updated_at=? WHERE out_trade_no=?",(raw,first(params,['trade_no']),'已自动到账到中转站账号',t,out_trade_no))
                    else:
                        code=create_sub2api_redeem_code(out_trade_no, po['ldc_amount'], po['usd_value'], po['user_sub'], po['username'] or '')
                        c.execute("UPDATE purchase_orders SET status='issued', code=?, raw_notify=?, credit_trade_no=?, delivery_message=?, updated_at=? WHERE out_trade_no=?",(code,raw,first(params,['trade_no']),'未关联中转站账号，已生成兑换码',t,out_trade_no))
                except Exception as e:
                    sys.stderr.write('[auto-credit] failed, fallback to redeem code: '+repr(e)+'\n')
                    try:
                        code=create_sub2api_redeem_code(out_trade_no, po['ldc_amount'], po['usd_value'], po['user_sub'], po['username'] or '')
                        c.execute("UPDATE purchase_orders SET status='issued', code=?, raw_notify=?, credit_trade_no=?, delivery_message=?, updated_at=? WHERE out_trade_no=?",(code,raw,first(params,['trade_no']),'自动到账失败，已生成兑换码：'+str(e),t,out_trade_no))
                    except Exception as e2:
                        c.execute("UPDATE purchase_orders SET status='pending_no_stock', raw_notify=?, credit_trade_no=?, delivery_message=?, updated_at=? WHERE out_trade_no=?",(str(e)+'; fallback: '+str(e2)+'\n'+raw,first(params,['trade_no']),'自动到账和兑换码兜底均失败',t,out_trade_no))
                c.commit(); return 'success'
    ok, oid=derive_order_key(params)
    issue_legacy(ok,oid,params,method,headers,client)
    return 'success'

PURCHASE_ORDER_RESULT_FIELDS='out_trade_no,plan,ldc_amount,usd_value,username,status,code,credit_trade_no,sub2api_user_id,sub2api_user_email,delivery_message,created_at,updated_at'

def reconcile_credit_order(out_trade_no, force=False):
    with conn() as c:
        po=c.execute('SELECT out_trade_no,ldc_amount,status FROM purchase_orders WHERE out_trade_no=?',(out_trade_no,)).fetchone()
    if not po or po['status'] != 'created' or not claim_credit_query(out_trade_no,force):
        return False
    try:
        remote=query_credit_order(out_trade_no)
        if str(remote.get('status','')) != '1':
            return False
        paid=str(remote.get('money') or '')
        if not paid or float(paid) != float(po['ldc_amount']):
            raise RuntimeError('Credit query returned a different amount')
        trade_no=str(remote.get('trade_no') or '')
        if not trade_no:
            raise RuntimeError('Credit query returned no trade number')
        params={
            'money':paid,
            'name':str(remote.get('name') or ''),
            'out_trade_no':str(out_trade_no),
            'pid':str(remote.get('pid') or LDC_PID),
            'trade_no':trade_no,
            'trade_status':'TRADE_SUCCESS',
            'type':str(remote.get('type') or 'epay'),
        }
        params['sign']=epay_sign(params,LDC_KEY)
        params['sign_type']='MD5'
        if handle_credit_notify(params,'QUERY',{},'127.0.0.1') != 'success':
            raise RuntimeError('Credit reconciliation was rejected')
        return True
    except Exception as e:
        sys.stderr.write('[credit-reconcile] {}: {}\n'.format(out_trade_no,repr(e)))
        return False

def load_purchase_order(order_id, reconcile=False):
    with conn() as c:
        po=c.execute(
            'SELECT '+PURCHASE_ORDER_RESULT_FIELDS+' FROM purchase_orders WHERE out_trade_no=? OR credit_trade_no=?',
            (order_id,order_id),
        ).fetchone()
    if po and reconcile and po['status'] == 'created':
        reconcile_credit_order(po['out_trade_no'])
        with conn() as c:
            po=c.execute(
                'SELECT '+PURCHASE_ORDER_RESULT_FIELDS+' FROM purchase_orders WHERE out_trade_no=? OR credit_trade_no=?',
                (order_id,order_id),
            ).fetchone()
    return po

class H(BaseHTTPRequestHandler):
    server_version='lklb-code-shop/1.1'
    def log_message(self, fmt, *args): sys.stderr.write('%s - - [%s] %s\n'%(self.client_address[0], self.log_date_time_string(), fmt%args))
    def read_params(self):
        u=urlparse(self.path); params=flatten(parse_qs(u.query,keep_blank_values=True))
        if self.command=='POST':
            n=int(self.headers.get('Content-Length','0') or '0')
            body=self.rfile.read(min(n,2*1024*1024)) if n else b''
            ct=self.headers.get('Content-Type','')
            if 'application/json' in ct:
                try:
                    data=json.loads(body.decode('utf-8') or '{}')
                    if isinstance(data,dict): params.update(data)
                except Exception: params['_json_error']='invalid json'
            else:
                params.update(flatten(parse_qs(body.decode('utf-8','replace'),keep_blank_values=True)))
        return params
    def send_result(self,res, head_only=False):
        code,headers,body=res
        self.send_response(code)
        for k,v in headers: self.send_header(k,v)
        self.send_header('Content-Length',str(len(body)))
        self.end_headers()
        if not head_only: self.wfile.write(body)
    def is_admin(self, params): return bool(ADMIN_TOKEN) and ((self.headers.get('X-Admin-Token') or params.get('token') or '')==ADMIN_TOKEN)
    def current_user(self): return verify_session(get_cookie(self.headers,'lklb_session'))
    def do_HEAD(self): self.handle_any(head_only=True)
    def do_GET(self): self.handle_any()
    def do_POST(self): self.handle_any()
    def handle_any(self, head_only=False):
        old_send=self.send_result
        self.send_result=lambda res: old_send(res, head_only=head_only)
        try:
            init_db(); u=urlparse(self.path); path=u.path; params=self.read_params()
            if path=='/health': return self.send_result(json_bytes({'status':'ok','service':'lklb-code-shop','time':now_iso()}))
            if path=='/buy': return self.send_result(self.buy(params))
            if path=='/buy/start': return self.send_result(self.buy_start(params))
            if path=='/api/linuxdo-connect/login': return self.send_result(self.connect_login(params))
            if path=='/api/linuxdo-connect/callback': return self.send_result(self.connect_callback(params))
            if path=='/api/linuxdo-connect/logout': return self.send_result(redirect_bytes('/buy', [('Set-Cookie', clear_cookie('lklb_session'))]))
            if path=='/api/linuxdo-credit/notify':
                result=handle_credit_notify(params,self.command,self.headers,self.client_address[0])
                return self.send_result(text_bytes(result, 200 if result=='success' else 400))
            if path=='/api/order': return self.send_result(self.api_order(params))
            if path=='/buy/result': return self.send_result(self.redeem(params))
            if path=='/admin/import-codes' and self.command=='POST': return self.send_result(self.import_codes(params))
            if path=='/admin/stats': return self.send_result(self.stats(params))
            if path=='/admin/orders': return self.send_result(self.admin_orders(params))
            return self.send_result(text_bytes('not found',404))
        except Exception:
            traceback.print_exc(); return self.send_result(text_bytes('internal error',500))
    def connect_login(self,params):
        if not CONNECT_CLIENT_ID or not CONNECT_CLIENT_SECRET: return text_bytes('LinuxDO Connect 未配置',500)
        nxt=first(params,['next']) or '/buy'
        if not nxt.startswith('/'): nxt='/buy'
        state=secrets.token_urlsafe(24)
        st=sign_session({'state':state,'next':nxt})
        qs=urlencode({'client_id':CONNECT_CLIENT_ID,'redirect_uri':CONNECT_REDIRECT_URI,'response_type':'code','scope':'user','state':state})
        return redirect_bytes(CONNECT_AUTHORIZE_URL+'?'+qs, [('Set-Cookie', cookie('lklb_oauth_state',st,600))])
    def connect_callback(self,params):
        code_value=first(params,['code']); state=first(params,['state'])
        sub2api_state=decoded_cookie(self.headers,'linuxdo_oauth_state')
        lklb_state_cookie=get_cookie(self.headers,'lklb_oauth_state')
        if code_value and state and ((sub2api_state and hmac.compare_digest(sub2api_state,state)) or not lklb_state_cookie):
            return redirect_bytes('/api/v1/auth/oauth/linuxdo/callback?'+urlencode(params, doseq=True))
        st=verify_session(lklb_state_cookie)
        if not code_value or not state or not st or st.get('state')!=state:
            return text_bytes('登录状态无效，请重新登录',400,'text/plain; charset=utf-8',[('Set-Cookie',clear_cookie('lklb_oauth_state'))])
        user=exchange_connect_code(code_value)
        sub=str(user.get('sub') or user.get('id') or user.get('user_id') or '')
        if not sub: return text_bytes('登录失败：未获取到用户 ID',500)
        username=str(user.get('username') or user.get('login') or user.get('name') or sub)
        handoff=handoff_cookie(self.headers)
        sess=sign_session(merge_handoff_into_user({'sub':sub,'username':username,'name':user.get('name') or username,'avatar_url':user.get('avatar_url') or ''}, handoff))
        return redirect_bytes(st.get('next') or '/buy', [('Set-Cookie', cookie('lklb_session',sess)), ('Set-Cookie', clear_cookie('lklb_oauth_state'))])
    def buy(self,params):
        extra_headers=[]
        handoff_token=first(params,['handoff'])
        if handoff_token:
            try:
                handoff=verify_sub2api_handoff(handoff_token)
                extra_headers.append(('Set-Cookie', handoff_set_cookie(handoff)))
                current=self.current_user()
                if current and current.get('sub'):
                    extra_headers.append(('Set-Cookie', cookie('lklb_session', sign_session(merge_handoff_into_user(current, handoff)))))
            except Exception as e:
                sys.stderr.write('[handoff] verify failed: '+repr(e)+'\\n')
        user=self.current_user()
        with conn() as c:
            stock={r['plan']:r['n'] for r in c.execute("SELECT plan,COUNT(*) n FROM codes WHERE status='available' GROUP BY plan")}
            issued={}
            used_usd=0
            if user:
                issued={}
                used_usd=unified_ldc_used_usd(c, user)
        remain=remaining_promo_usd(used_usd)
        login_html=""
        if user:
            login_html="<div class='login-note'>已登录：{}</div> <a class='btn2' href='/api/linuxdo-connect/logout'>退出</a>".format(html.escape(user.get('username','')))
        else:
            login_html="<div class='login-note'>点击购买时使用 LinuxDO 登录</div>"
        body=["<div class='hero'><div><h1>LKLB LDC 充值</h1><p class='subtitle'>阶梯计价：每个账号前 100 LDC 可兑换 10刀额度；超出部分按 40 LDC 兑换 1刀额度。</p>{}</div></div>".format(login_html)]
        body.append("<div class='plans'>")
        fixed_ldc = [('10','10 LDC'), ('50','50 LDC'), ('100','100 LDC')]
        for amount,label in fixed_ldc:
            body.append("<div class='plan'>")
            body.append("<h3>LDC 充值</h3>")
            body.append("<div class='price'>{}<span class='unit'>LDC</span></div>".format(html.escape(amount)))
            body.append("<p class='normal'>前 100 LDC 享优惠折算</p>")
            body.append("<form method='post' action='/buy/start'><input type='hidden' name='plan' value='custom'><input type='hidden' name='ldc' value='{}'><button type='submit'>购买</button></form>".format(html.escape(amount)))
            body.append("</div>")
        body.append("<div class='plan custom'>")
        body.append("<div class='custom-copy'><h3>自定义 LDC</h3><div class='price'>任意数量</div><p class='normal'>输入 LDC 数量，按阶梯规则兑换额度。</p></div>")
        body.append("<form method='post' action='/buy/start'><input type='hidden' name='plan' value='custom'><input name='ldc' inputmode='numeric' pattern='[0-9]*' placeholder='输入 LDC，例如 50'><button type='submit'>购买</button></form>")
        body.append("</div><p class='foot'>从中转站进入并完成 LinuxDO 登录后，支付成功会优先自动到账；无法自动到账时生成兑换码兜底。</p>")
        return text_bytes(html_page('LKLB LDC 充值',''.join(body)),200,'text/html; charset=utf-8',extra_headers)
    def buy_start(self,params):
        if self.command not in ('POST','GET'): return redirect_bytes('/buy')
        user=self.current_user()
        if not user:
            plan=first(params,['plan'])
            if plan == 'custom':
                ldc=first(params,['ldc'])
                if ldc:
                    nxt='/buy/start?plan='+quote(plan)+'&ldc='+quote(ldc)
                else:
                    nxt='/buy/start?plan='+quote(plan)+'&amount='+quote(first(params,['amount']))
            else:
                nxt='/buy/start?plan='+quote(plan) if plan else '/buy'
            return redirect_bytes('/api/linuxdo-connect/login?next='+quote(nxt))
        plan=first(params,['plan'])
        if plan != 'custom' and plan not in PLANS: return text_bytes('invalid plan',400)
        if not LDC_PID or not LDC_KEY: return text_bytes('LinuxDO Credit 未配置',500)
        try:
            if 'ldc' in params:
                requested_ldc=parse_ldc_amount(params)
                requested_plan='ldc{}'.format(str(requested_ldc).rstrip('0').rstrip('.'))
            else:
                # 向后兼容旧链接：传 amount/usd 时仍按“额度”反推应付 LDC
                usd_req=parse_custom_usd(params) if plan == 'custom' else PLANS[plan]['usd_value']
                requested_ldc=None
                requested_plan='usd{}'.format(usd_req) if plan == 'custom' else plan
        except ValueError as e:
            return text_bytes(html_page('数量无效',"<h1>数量无效</h1><div class='err'>{}</div><a class='btn' href='/buy'>返回</a>".format(html.escape(str(e)))),400,'text/html; charset=utf-8')
        t=now_iso()
        with conn() as c:
            out_trade_no=make_order_no(requested_plan)
            used_usd=unified_ldc_used_usd(c, user)
            if requested_ldc is not None:
                ldc_amount=f'{requested_ldc:.2f}'
                usd_value=calculate_usd_from_ldc(requested_ldc, used_usd)
            else:
                usd_value=usd_req
                ldc_amount=calculate_ldc_amount(usd_value, used_usd)
            title='LKLB 中转站 {} LDC'.format(ldc_amount)
            c.execute('INSERT INTO purchase_orders(out_trade_no,plan,ldc_amount,usd_value,user_sub,username,status,created_at,updated_at,sub2api_user_id,sub2api_user_email) VALUES(?,?,?,?,?,?,?,?,?,?,?)',(out_trade_no,plan,ldc_amount,usd_value,user['sub'],user.get('username',''),'created',t,t,int(user.get('sub2api_user_id') or 0) or None,user.get('sub2api_user_email') or None))
        try:
            pay_url=create_credit_payment(out_trade_no,plan,ldc_amount,title)
            with conn() as c: c.execute('UPDATE purchase_orders SET credit_pay_url=?,updated_at=? WHERE out_trade_no=?',(pay_url,now_iso(),out_trade_no))
            return redirect_bytes(pay_url)
        except Exception as e:
            with conn() as c: c.execute("UPDATE purchase_orders SET status='create_failed',raw_notify=?,updated_at=? WHERE out_trade_no=?",(str(e),now_iso(),out_trade_no))
            return text_bytes(html_page('创建订单失败',"<h1>创建订单失败</h1><div class='err'>{}</div><a class='btn' href='/buy'>返回</a>".format(html.escape(str(e)))),502,'text/html; charset=utf-8')
    def api_order(self,params):
        oid=first(params,['order_id','out_trade_no','trade_no','order_no','id'])
        if not oid: return json_bytes({'ok':False,'error':'missing order_id'},400)
        po=load_purchase_order(oid,reconcile=True)
        if po: return json_bytes({'ok':True,'order':dict(po)})
        with conn() as c:
            row=c.execute('SELECT order_key,linuxdo_order_id,status,code,created_at,updated_at,buyer_key,buyer_label FROM orders WHERE order_key=? OR linuxdo_order_id=?',(oid,oid)).fetchone()
        if not row: return json_bytes({'ok':False,'error':'order not found'},404)
        return json_bytes({'ok':True,'order':dict(row)})
    def redeem(self,params):
        oid=first(params,['order_id','out_trade_no','trade_no','order_no','id'])
        row=None; err=''
        if oid:
            r=load_purchase_order(oid,reconcile=True)
            if r: row=dict(r); row['order_key']=row['out_trade_no']
        if oid and not row:
            with conn() as c:
                r=c.execute('SELECT order_key,linuxdo_order_id,status,code,created_at,updated_at,buyer_key,buyer_label FROM orders WHERE order_key=? OR linuxdo_order_id=?',(oid,oid)).fetchone()
                row=dict(r) if r else None
            if not row: err='未找到订单，请稍后刷新或检查订单号。'
        body=['<h1>订单兑换码</h1>']
        if row:
            body.append('<p class="muted">订单号：%s</p>'%html.escape(row['order_key']))
            if row['status']=='credited':
                body.append("<div class='ok'>支付成功，额度已自动到账到您的 Link-Label 中转站账号。</div><a class='btn' href='/dashboard'>返回控制台</a>")
            elif row['status']=='issued' and row.get('code'):
                body.append("<p>支付成功，您的兑换码：</p><div class='code'>%s</div><a class='btn' href='/redeem'>前往 Link-Label 兑换</a>"%html.escape(row['code']))
            elif row['status']=='pending_no_stock': body.append("<div class='err'>支付已记录，但当前兑换码库存不足。请联系管理员补发。</div>")
            elif row['status'] in ('duplicate_buyer','duplicate_user'): body.append("<div class='err'>该 LinuxDO 账号已购买过该档位，本次不再发放兑换码。</div>")
            elif row['status']=='created': body.append(created_order_processing_html(row['order_key']))
            elif row['status']=='create_failed': body.append("<div class='err'>订单创建失败，请返回重新下单或联系管理员。状态：%s</div>"%html.escape(row['status']))
            else: body.append("<div class='err'>订单状态：%s</div>"%html.escape(row['status']))
        else:
            if err: body.append('<div class="err">%s</div>'%html.escape(err))
            body.append("<p class='muted'>如果支付后没有自动显示，请输入订单号查询。</p><form method='get' action='/buy/result'><input name='order_id' placeholder='订单号 / out_trade_no / trade_no'><button type='submit'>查询兑换码</button></form><p><a class='btn' href='/buy'>去购买</a></p>")
        return text_bytes(html_page('订单兑换码',''.join(body)),404 if err else 200,'text/html; charset=utf-8')
    def import_codes(self,params):
        if not self.is_admin(params): return json_bytes({'ok':False,'error':'unauthorized'},401)
        plan=first(params,['plan']) or 'usd1'
        if plan not in PLANS: return json_bytes({'ok':False,'error':'invalid plan'},400)
        v=params.get('codes') or params.get('code') or ''; codes=[]
        if isinstance(v,list):
            for x in v: codes += str(x).replace(',', '\n').splitlines()
        else: codes=str(v).replace(',', '\n').splitlines()
        codes=[c.strip() for c in codes if c.strip()]
        batch=str(params.get('batch') or time.strftime('%Y%m%d-%H%M%S')); imported=skipped=0; t=now_iso()
        with conn() as c:
            for code in codes:
                try: c.execute('INSERT INTO codes(code,status,batch,imported_at,plan) VALUES(?,?,?,?,?)',(code,'available',batch,t,plan)); imported+=1
                except sqlite3.IntegrityError: skipped+=1
        return json_bytes({'ok':True,'imported':imported,'skipped':skipped,'batch':batch,'plan':plan})
    def stats(self,params):
        if not self.is_admin(params): return json_bytes({'ok':False,'error':'unauthorized'},401)
        with conn() as c:
            cs=[dict(r) for r in c.execute('SELECT plan,status,COUNT(*) n FROM codes GROUP BY plan,status ORDER BY plan,status')]
            os_={r['status']:r['n'] for r in c.execute('SELECT status,COUNT(*) n FROM orders GROUP BY status')}
            ps=[dict(r) for r in c.execute('SELECT plan,status,COUNT(*) n FROM purchase_orders GROUP BY plan,status ORDER BY plan,status')]
            recent=[dict(r) for r in c.execute('SELECT out_trade_no,plan,username,status,code,created_at FROM purchase_orders ORDER BY id DESC LIMIT 10')]
        return json_bytes({'ok':True,'codes':cs,'legacy_orders':os_,'purchase_orders':ps,'recent_orders':recent})


    def admin_orders(self,params):
        if not self.is_admin(params): return json_bytes({'ok':False,'error':'unauthorized'},401)
        try: page=max(1,int(first(params,['page']) or 1))
        except Exception: page=1
        try: page_size=int(first(params,['page_size']) or 20)
        except Exception: page_size=20
        page_size=max(1,min(page_size,100))
        status=(first(params,['status']) or '').strip()
        search=(first(params,['search']) or first(params,['q']) or '').strip()
        where=[]; args=[]
        if status:
            where.append('status=?'); args.append(status)
        if search:
            like='%'+search+'%'
            where.append('(out_trade_no LIKE ? OR username LIKE ? OR IFNULL(code,"") LIKE ? OR IFNULL(sub2api_user_email,"") LIKE ? OR IFNULL(user_sub,"") LIKE ? OR IFNULL(credit_trade_no,"") LIKE ?)')
            args.extend([like,like,like,like,like,like])
        where_sql=(' WHERE '+ ' AND '.join(where)) if where else ''
        with conn() as c:
            total=c.execute('SELECT COUNT(*) n FROM purchase_orders'+where_sql, args).fetchone()['n']
            offset=(page-1)*page_size
            rows=c.execute(
                'SELECT id,out_trade_no,plan,ldc_amount,usd_value,user_sub,username,status,credit_trade_no,code,sub2api_user_id,sub2api_user_email,delivery_message,created_at,updated_at '
                'FROM purchase_orders'+where_sql+' ORDER BY id DESC LIMIT ? OFFSET ?',
                args+[page_size,offset]
            ).fetchall()
            items=[dict(r) for r in rows]
            pages=(int(total)+page_size-1)//page_size if page_size else 0
            status_counts={r['status']:r['n'] for r in c.execute('SELECT status, COUNT(*) n FROM purchase_orders GROUP BY status')}
        return json_bytes({'ok':True,'items':items,'total':int(total),'page':page,'page_size':page_size,'pages':pages,'status_counts':status_counts})

class ThreadingHTTPServer(ThreadingMixIn, HTTPServer):
    daemon_threads = True

if __name__=='__main__':
    init_db(); print('listening on %s:%s' % (HOST, PORT), flush=True); ThreadingHTTPServer((HOST,PORT),H).serve_forever()
