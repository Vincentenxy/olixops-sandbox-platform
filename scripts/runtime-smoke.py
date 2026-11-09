#!/usr/bin/env python3
"""Verify real conversation restoration. Keeps the test workspace/PVC; deletes compute."""
import argparse
import base64
import json
import pathlib
import time
import urllib.error
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--real', action='store_true', required=True)
    parser.add_argument('--url', default='http://127.0.0.1:18090')
    args = parser.parse_args()
    root = pathlib.Path(__file__).resolve().parents[1]
    client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    token = ''
    evidence = {'sandboxes': [], 'checks': []}

    def call(route, body=None):
        req = urllib.request.Request(args.url + '/api/v1/' + route,
            data=None if body is None else json.dumps(body).encode(),
            headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
        with client.open(req, timeout=100) as response:
            result = json.load(response)
        if result['code'] != 0:
            raise RuntimeError(result)
        return result['data']

    def wait_for(route, body, predicate, label):
        deadline = time.monotonic() + 180
        last = None
        while time.monotonic() < deadline:
            last = call(route, body)
            if predicate(last):
                return last
            if route == 'run/status' and last['state'] in ('Failed', 'Released'):
                raise RuntimeError(last)
            time.sleep(2)
        raise TimeoutError(f'{label}: {last}')

    def persist():
        (root / '.local/runtime-smoke.json').write_text(json.dumps(evidence, ensure_ascii=False, indent=2))

    def check(message):
        evidence['checks'].append(message)
        print('PASS:', message, flush=True)
        persist()

    token = call('pub/auth/login', {'username': 'vincent', 'password': (root / '.local/local-password.txt').read_text().strip()})['accessToken']
    projects = call('projects')
    project = next((p for p in projects if p['name'] == '对话恢复验收'), None)
    if project is None:
        project = call('project/create', {'name': '对话恢复验收'})
    evidence['projectId'] = project['id']
    workspace = call('workspace/create', {'projectId': project['id'], 'name': 'phase2-' + time.strftime('%m%d-%H%M%S'), 'requestKey': str(uuid.uuid4())})
    evidence['workspaceId'] = workspace['id']
    evidence['pvcName'] = workspace['pvcName']
    evidence['pvcUid'] = workspace['pvcUid']
    conversation = call('conversation/register', {'projectId': project['id'], 'integrationId': 'runtime-smoke', 'externalId': str(uuid.uuid4()), 'title': '跨实例恢复代码验收'})
    conversation = call('conversation/workspace', {'id': conversation['id'], 'workspaceId': workspace['id'], 'expectedVersion': 0})
    evidence['conversationId'] = conversation['id']
    persist()
    contexts = []

    def start():
        request = {'conversationId': conversation['id'], 'expectedVersion': conversation['version'], 'image': 'python:3.12', 'ttl': 600, 'requestKey': str(uuid.uuid4())}
        run = call('conversation/continue', request)
        contexts.append(run['id'])
        repeat = call('conversation/continue', request)
        assert repeat['id'] == run['id'], repeat
        run = wait_for('run/status', {'id': run['id']}, lambda r: r['state'] == 'Ready', 'prepare')
        evidence['sandboxes'].append(run['sandboxId'])
        persist()
        return run

    def action(run, kind, **fields):
        return call('sandbox/action', {'id': run['sandboxId'], 'runContextId': run['id'], 'action': kind, **fields})

    try:
        first = start()
        check('first continue becomes Ready; duplicate request reuses context')
        text = 'persistent code / ' + str(uuid.uuid4()) + '\n'
        path = '/workspace/phase2-persistent.txt'
        action(first, 'write', path=path, content=base64.b64encode(text.encode()).decode())
        assert base64.b64decode(action(first, 'read', path=path)['content']).decode() == text
        check('first runtime writes and reads the workspace file')
        call('sandbox/action', {'id': first['sandboxId'], 'action': 'delete'})
        wait_for('run/status', {'id': first['id']}, lambda r: r['state'] == 'Invalidated', 'old context invalidation')
        second = start()
        assert second['sandboxId'] != first['sandboxId']
        assert second['attachmentGeneration'] > first['attachmentGeneration']
        assert base64.b64decode(action(second, 'read', path=path)['content']).decode() == text
        current = next(w for w in call('workspace/list', {'projectId': project['id']}) if w['id'] == workspace['id'])
        assert current['pvcUid'] == workspace['pvcUid']
        evidence['pvName'] = current['pvName']
        check('new sandbox attaches the same PVC UID and reads original contents')
        try:
            action(first, 'write', path=path, content=base64.b64encode(b'wrong').decode())
        except urllib.error.HTTPError as error:
            assert error.code == 409
        else:
            raise AssertionError('old context accepted a write')
        assert base64.b64decode(action(second, 'read', path=path)['content']).decode() == text
        check('old context is rejected; original contents stay intact')
        call('run/release', {'id': second['id']})
        released = call('run/status', {'id': second['id']})
        assert released['state'] == 'Released'
        check('release invalidates context without deleting files or compute')
    finally:
        for run_id in contexts:
            current = call('run/status', {'id': run_id})
            if current['sandboxId']:
                call('sandbox/action', {'id': current['sandboxId'], 'action': 'delete'})
        # Consumer exit can lag behind the OpenSandbox delete response.
        wait_for('workspace/list', {'projectId': project['id']},
                 lambda rows: any(w['id'] == workspace['id'] and not w['activeSandboxId'] for w in rows),
                 'compute cleanup')
        check('compute is cleaned up; test workspace, PVC and file are retained')


if __name__ == '__main__':
    main()
