#!/usr/bin/env python3
"""Explicit real-backend smoke test: creates one 10-minute sandbox and deletes it."""
import argparse, base64, json, pathlib, time, urllib.request, urllib.error, uuid
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--real',action='store_true',required=True,help='acknowledge creation of a real sandbox')
p.add_argument('--url',default='http://127.0.0.1:18090')
p.add_argument('--image',default='python:3.12')
a=p.parse_args()
root=pathlib.Path(__file__).resolve().parents[1]
token=''
client=urllib.request.build_opener(urllib.request.ProxyHandler({}))
def call(route,body=None):
 req=urllib.request.Request(a.url+'/api/v1/'+route,data=None if body is None else json.dumps(body).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
 with client.open(req,timeout=150) as r:v=json.load(r)
 if v['code']!=0:raise RuntimeError(v)
 return v['data']
login=call('pub/auth/login',{'username':'vincent','password':(root/'.local/local-password.txt').read_text().strip()})
token=login['accessToken']
for attempt in range(20):
 try:
  projects=call('projects');break
 except (urllib.error.URLError,urllib.error.HTTPError):
  if attempt==19:raise
  time.sleep(1)
project=next((p for p in projects if p['name']=='平台验收项目'),None)
if project is None:project=call('project/create',{'name':'平台验收项目'})
result=call('sandbox/create',{'projectId':project['id'],'name':'olixops-smoke','image':a.image,'ttl':600,'requestKey':str(uuid.uuid4())})
b=result['sandbox'];sid=b['id'];print('Platform sandbox:',sid,'state:',b['state'],flush=True)
def action(kind,**kw):return call('sandbox/action',{'id':sid,'action':kind,**kw})
try:
 for _ in range(40):
  b=action('sync');print('State:',b['state'],flush=True)
  if b['state']=='Running':break
  if b['state'] in ('Failed','Error','Deleted','Expired','Unknown'):raise RuntimeError('not ready: '+b['state'])
  time.sleep(2)
 else:raise TimeoutError('sandbox readiness timeout')
 command=action('command',command='mkdir -p /workspace && echo olixops-smoke-ready')
 assert 'olixops-smoke-ready' in json.dumps(command), command
 print('Command: PASS',flush=True)
 text='hello from OlixOps 测试\n'
 action('write',path='/workspace/smoke.txt',content=base64.b64encode(text.encode()).decode())
 read=action('read',path='/workspace/smoke.txt')
 assert base64.b64decode(read['content']).decode()==text
 print('File upload/download: PASS',flush=True)
 action('command',command='python -m http.server 8000 --bind 0.0.0.0 --directory /workspace',background=True)
 for _ in range(10):
  try:
   response=action('preview',port=8000,path='/smoke.txt')
   if response['status']==200:break
  except urllib.error.HTTPError:pass
  time.sleep(1)
 else:raise RuntimeError('HTTP preview not ready')
 assert response['body']==text,response
 print('Application HTTP proxy: PASS',flush=True)
 action('remove',path='/workspace/smoke.txt')
 print('File delete: PASS',flush=True)
finally:
 action('delete')
 final=action('sync')
 assert final['state']=='Deleted',final
 print('Sandbox cleanup: PASS',flush=True)
