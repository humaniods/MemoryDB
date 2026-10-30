"""Actual process + network + Python SDK integration; no persistent test data."""
import os, subprocess, tempfile, sys, time, socket
from pathlib import Path
ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'sdk'))
from memorydb import MemoryDB, MemoryDBError

def expect_status(status, fn):
    try: fn()
    except MemoryDBError as e: assert e.status == status, (e.status, str(e))
    else: raise AssertionError(f'Expected HTTP {status}')

with tempfile.TemporaryDirectory() as td:
    token = 'development-token-for-verification'
    binary = str(Path(td) / 'memorydb')
    subprocess.run(['go', 'build', '-buildvcs=false', '-o', binary, '.'], cwd=ROOT, check=True)
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0)); port = sock.getsockname()[1]
    args = [binary, '-addr', f'127.0.0.1:{port}', '-data', td+'/test.wal']
    def start():
        proc = subprocess.Popen(args, env={**os.environ, 'MEMORYDB_TOKEN':token}, stdout=subprocess.DEVNULL)
        for _ in range(100):
            try: db.request('GET','health/'); return proc
            except OSError: time.sleep(.05)
        proc.terminate(); proc.wait(); raise AssertionError('Server did not start')
    db = MemoryDB(f'http://127.0.0.1:{port}', token=token)
    p = start()
    try:
        expect_status(401, lambda: MemoryDB(f'http://127.0.0.1:{port}',token='invalid').collections.all())
        record=db.objects.create(kind='fact', content='HTTP SDK semantic test', vector=[1,0])
        assert db.objects.get(pk=record['id'])['content']==record['content']
        assert len(db.objects.search(vector=[1,0]))==1
        updated=db.objects.update(pk=record['id'], version=1, content='Updated SDK record')
        assert updated['version']==2
        expect_status(409, lambda: db.objects.update(pk=record['id'],version=1,content='stale'))
        assert len(db.objects.history(pk=record['id']))==2
        db.objects.delete(pk=record['id'],version=2)
        assert not db.objects.filter()
        db.collections.create('users', fields={'email':{'type':'string','required':True},'age':{'type':'integer'}},unique=['email'],indexes=['age'])
        users = db.collection('users').objects
        a = users.create(email='a',age=20,idempotency_key='create-a')
        assert users.create(email='a',age=20,idempotency_key='create-a')['id']==a['id']
        expect_status(409, lambda: users.create(email='a',age=21))
        users.bulk_create([{'email':'b','age':30},{'email':'c','age':40}],idempotency_key='bulk')
        assert users.filter(age__gte=30).count()==2
        assert [r['data']['age'] for r in users.filter(age__gte=20).order_by('-age')]==[40,30,20]
        assert users.filter(age=30).only('email').first()['data']=={'email':'b'}
        page=users.all().page(limit=1); assert page['next_cursor']
        assert len(users.all().page(cursor=page['next_cursor'],limit=1)['results'])==1
        expect_status(409, lambda: users.bulk_create([{'email':'d','age':1},{'email':'a','age':2}]))
        assert users.all().count()==3
        users.update(pk=a['id'],version=1,age=21)
        expect_status(409, lambda: users.all().page(cursor=page['next_cursor'],limit=1))
        users.delete(pk=a['id'],version=2)
        expect_status(404,lambda: users.get(pk=a['id']))
        # Widen a live collection the way ALTER TABLE ADD COLUMN does, then prove the new field
        # is queryable and that a schema the current rows cannot satisfy is refused.
        db.collections.alter('users', fields={'nickname':{'type':'string'}}, indexes=['nickname'])
        users.create(email='n@example.com', nickname='Nina')
        assert users.filter(nickname__icontains='nin').first()['data']['email']=='n@example.com'
        expect_status(409, lambda: db.collections.alter('users', fields={'nickname':{'type':'integer'}}))
        expect_status(404, lambda: db.collections.alter('nowhere', fields={'tag':{'type':'string'}}))
        try:
            db.collections.alter('users', fields={'tag':{'type':'string','required':True}}); raise AssertionError('rows without tag should block a required field')
        except MemoryDBError as e: assert e.status in (400,409), e.status
        assert {r['data'].get('nickname') for r in users.all()} >= {'Nina'}
        results=db.ingest([{'role':'user','content':'I prefer Hindi'}],facts=[{'subject':'me','predicate':'language','value':'Hindi','quote':'I prefer Hindi','message_index':0,'confidence':1}])
        assert {r['kind'] for r in results} >= {'chat','fact','summary'}
        job=db.queue_ingest(messages=[{'role':'user','content':'Queued conversation'}])
        assert db.request('POST',f"jobs/{job['id']}/run/",{})['metadata']['status']=='done'
        p.terminate();p.wait(timeout=5);p=start()
        assert users.all().count()==3
        # The widened schema is part of the journal, not a client-side wish.
        assert db.collections.schema('users')['data']['fields']['nickname']['type']=='string'
        assert users.filter(nickname='Nina').count()==1
        backup=td+'/snapshot.bak';p.terminate();p.wait(timeout=5)
        for extra in (['-verify'],['-backup',backup],['-compact','1'],['-verify']):
            subprocess.run([binary,'-data',td+'/test.wal',*extra],check=True,capture_output=True)
        subprocess.run([binary,'-data',td+'/restored.wal','-restore',backup],check=True,capture_output=True)
        print('HTTP + Python SDK: auth, CRUD, schema, indexes, pagination, transaction rollback, retry, ingestion, jobs, restart, backup/restore: PASS')
    finally:
        if p.poll() is None:p.terminate();p.wait(timeout=5)
