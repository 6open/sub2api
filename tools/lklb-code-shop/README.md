# LKLB Code Shop

This directory stores the LKLB LinuxDO Credit shop service that is deployed to
`/opt/lklb-code-shop/app_stdlib.py` on ali98.

Do not commit production `.env` files or secrets. Deploy by copying
`app_stdlib.py` to the server and restarting `lklb-code-shop`.

Quick regression test:

```bash
python3 -m pytest tools/lklb-code-shop/tests/test_promo_bucket.py
```
