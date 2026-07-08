#!/usr/bin/env python3
from pathlib import Path

SCRIPT = Path('deploy/lklb-deploy-ali98.sh')


def read_script():
    assert SCRIPT.exists(), 'deploy/lklb-deploy-ali98.sh should exist'
    return SCRIPT.read_text(encoding='utf-8')


def test_deploy_script_uses_debian_go_image_without_apk_add():
    s = read_script()
    assert 'golang:1.26.4' in s
    assert '/usr/local/go/bin/go build' in s
    assert 'apk add' not in s


def test_deploy_script_has_remote_backup_and_rollback():
    s = read_script()
    assert 'rollback_main_binary' in s
    assert 'podman cp "$APP_CONTAINER:/app/sub2api"' in s
    assert 'podman cp "$backup" "$APP_CONTAINER:/app/sub2api"' in s


def test_deploy_script_restarts_code_shop_via_systemd_without_orphan_process():
    s = read_script()
    assert 'restart_code_shop' in s
    assert 'sudo -n systemctl restart lklb-code-shop' in s
    assert 'systemctl is-active lklb-code-shop' in s
    assert 'pgrep -a -u admin -f "^/usr/bin/python3.11 /opt/lklb-code-shop/app_stdlib.py$"' in s
    assert 'nohup /usr/bin/python3.11 /opt/lklb-code-shop/app_stdlib.py' not in s


def test_deploy_script_verifies_health_purchase_and_buy():
    s = read_script()
    assert 'wait_for_remote_health' in s
    assert '/health' in s
    assert '/purchase' in s
    assert '/buy' in s
    assert 'PaymentView-' in s


def test_deploy_script_is_configurable_by_environment():
    s = read_script()
    for name in ['REMOTE_HOST', 'PUBLIC_BASE_URL', 'BUILD_IMAGE', 'GOPROXY', 'FRONTEND_TEST_CMD']:
        assert '${' + name + ':-' in s


def main():
    tests = [v for k, v in sorted(globals().items()) if k.startswith('test_')]
    failed = 0
    for test in tests:
        try:
            test()
            print(f'PASS {test.__name__}')
        except Exception as exc:
            failed += 1
            print(f'FAIL {test.__name__}: {exc}')
    if failed:
        raise SystemExit(1)


if __name__ == '__main__':
    main()
