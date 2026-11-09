"""Explicit real PVC persistence acceptance. Keeps the test workspace and conversation; cleans compute."""
import argparse
import base64
import hashlib
import json
import time
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--real', action='store_true', help='create a real workspace/PVC and two temporary sandboxes')
    args = parser.parse_args()
    if not args.real:
        parser.error('Use --real to create real resources. The persistent workspace will be retained.')
    token = ''
    def api(route, data=None):
        raw = None if data is None else json.dumps(data).encode()
        req = urllib.request.Request('http://127.0.0.1:18090/api/v1/'+route, raw, headers={'Content-Type':'application/json', **({'Authorization':'Bearer '+token} if token else {})})
        with opener.open(req, timeout=150) as r:
            result = json.load(r)
        assert result['code'] == 0, result
        return result['data']
    login = api('pub/auth/login', {'username':'vincent','password':(ROOT/'.local/local-password.txt').read_text().strip()})
    token = login['accessToken']
    sandbox = None
    workspace = None
    try:
        ns = api('namespace/current')
        project = api('project/create', {'name':'持久化工作区验收'})['id']
        workspace = api('workspace/create', {'projectId':project,'name':'长期代码验收','requestKey':'workspace-smoke-v1'})
        assert workspace['state']=='Available', workspace
        assert workspace['namespaceId']==ns['id']
        conversation = api('conversation/register', {'projectId':project,'integrationId':'local-ai','externalId':'persistent-smoke-1','title':'持久化验收对话'})
        conversation = api('conversation/workspace', {'id':conversation['id'],'workspaceId':workspace['id'],'expectedVersion':0})
        content = 'AI 开发代码跨实例保留\nprint("persistent workspace")\n'.encode()
        for number in (1,2):
            sandbox = api('sandbox/create', {'projectId':project,'workspaceId':workspace['id'],'name':f'persistence-{number}','image':'python:3.12','ttl':600,'requestKey':f'workspace-runtime-{number}'})['sandbox']
            assert sandbox['state']=='Running',sandbox
            if number==1:
                api('sandbox/action',{'id':sandbox['id'],'action':'write','path':'/workspace/persistence.txt','content':base64.b64encode(content).decode()})
            downloaded=api('sandbox/action',{'id':sandbox['id'],'action':'read','path':'/workspace/persistence.txt'})
            assert base64.b64decode(downloaded['content'])==content
            resolved=api('conversation/resolve',{'id':conversation['id']})
            current=resolved['workspace']
            assert current['pvcUid']==workspace['pvcUid'] and current['id']==workspace['id']
            print(json.dumps({'iteration':number,'namespace':ns['name'],'workspaceId':workspace['id'],'pvcUid':current['pvcUid'],'pvName':current['pvName'],'sha256':hashlib.sha256(content).hexdigest()},ensure_ascii=False), flush=True)
            api('sandbox/action',{'id':sandbox['id'],'action':'delete'})
            sandbox=None
            deadline=time.monotonic()+100
            while True:
                current=api('workspace/action',{'id':workspace['id'],'action':'sync'})
                if not current['activeSandboxId']:break
                if time.monotonic()>deadline:raise RuntimeError('old consumer still present; do not create next sandbox')
                time.sleep(2)
        print('PASS: two sandboxes used the same PVC, file hashes match; persistent workspace/conversation retained.',flush=True)
    finally:
        if sandbox:
            api('sandbox/action',{'id':sandbox['id'],'action':'delete'})
        api('auth/logout',{})

if __name__=='__main__':main()
