// Regenerate the README screenshots. Dev tooling only - nothing here runs in the server.
//   node tests/screenshots.cjs [--out .github/assets]
// Builds the server from this tree, seeds it with POST /api/v1/demo/ plus one ingestion,
// and photographs the console and the Studio in a real browser.
const {chromium}=require('playwright');
const {execFileSync,spawn}=require('child_process');
const fs=require('fs'),os=require('os'),path=require('path'),net=require('net');

const root=path.resolve(__dirname,'..');
const outArg=path.resolve(root,process.argv.includes('--out')?process.argv[process.argv.indexOf('--out')+1]:'.github/assets');

(async()=>{
const tmp=fs.mkdtempSync(path.join(os.tmpdir(),'memorydb-shots-'));
let server,browser;
const shots=[];
try{
 const binary=path.join(tmp,'memorydb');
 execFileSync('go',['build','-buildvcs=false','-o',binary,'.'],{cwd:root});
 const port=await new Promise(resolve=>{const s=net.createServer();s.listen(0,'127.0.0.1',()=>{const p=s.address().port;s.close(()=>resolve(p))})});
 const base=`http://127.0.0.1:${port}`,token='demo-token-not-a-secret';
 server=spawn(binary,['-addr',`127.0.0.1:${port}`,'-data',path.join(tmp,'shots.wal'),'-worker'],{env:{...process.env,MEMORYDB_TOKEN:token},stdio:'ignore'});
 let ready=false;for(let i=0;i<120;i++){try{if((await fetch(base)).ok){ready=true;break}}catch{}await new Promise(r=>setTimeout(r,100))}
 if(!ready)throw Error('server did not start');
 const api=(p,m,b)=>fetch(`${base}/api/v1/${p}`,{method:m,headers:{Authorization:`Bearer ${token}`,'Content-Type':'application/json'},body:b===undefined?undefined:JSON.stringify(b)}).then(async r=>{
   const j=await r.json();if(!r.ok)throw Error(`${p}: ${JSON.stringify(j)}`);return j;});
 await api('demo/','POST');
 const ingested=(await api('ingest/','POST',{source:'acme-crm',messages:[
   {role:'user',content:'Rani Textiles renews in January and they are on the scale plan with 85 seats.'},
   {role:'assistant',content:'Noted - renewal in January, scale plan, 85 seats.'}],
   facts:[{subject:'Rani Textiles',predicate:'renews_in',value:'January',quote:'Rani Textiles renews in January',message_index:0,confidence:0.95},
          {subject:'Rani Textiles',predicate:'seats',value:'85',quote:'with 85 seats',message_index:0,confidence:0.95}]})).results;
 const factRecord=ingested.find(m=>m.kind==='fact')||ingested[0];

 browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_PATH?{executablePath:process.env.CHROMIUM_PATH}:{}),args:['--no-sandbox']});
 const page=await browser.newPage({viewport:{width:1440,height:900},deviceScaleFactor:2});
 const errors=[];page.on('pageerror',e=>errors.push(e.message));
 const shoot=async(name)=>{await page.screenshot({path:path.join(outArg,name)});shots.push(name)};
 const settle=async()=>{await page.waitForFunction(()=>document.querySelector('#status').textContent.length>0);await page.waitForTimeout(250)};

 await page.goto(base);
 await page.locator('#connect').click();
 await page.locator('#token').fill(token);
 await page.locator('#authForm button').first().click();
 await page.waitForFunction(()=>document.querySelector('#status').textContent==='Connected');

 // 1. Document explorer: mirrored rows with their provenance under each one.
 await page.locator('#refresh').click();
 await page.waitForFunction(()=>[...document.querySelectorAll('#collection option')].some(o=>o.value==='customers'));
 await page.locator('#query').fill('{"projection":["id","name","region","mrr"],"order_by":"name","limit":4}');
 await page.selectOption('#collection','customers');
 await settle();
 await page.evaluate(()=>{document.querySelector('#query').scrollTop=0});
 await shoot('console-documents.png');

 // 2. Evidence-linked ingestion: the payload in, the records out.
 await page.locator('nav button[data-view="ingestion"]').click();
 await page.locator('#ingest').fill(JSON.stringify({source:'acme-crm',messages:[
   {role:'user',content:'Thar Solar moved to the growth plan and their MRR is 23000.'},
   {role:'assistant',content:'Recorded: growth plan, MRR 23000.'}],
   facts:[{subject:'Thar Solar',predicate:'plan',value:'growth',quote:'moved to the growth plan',message_index:0,confidence:0.95},
          {subject:'Thar Solar',predicate:'mrr',value:'23000',quote:'their MRR is 23000',message_index:0,confidence:0.95}]}));
 await page.locator('#runIngest').click();
 await page.waitForFunction(()=>document.querySelector('#ingestOutput').textContent.includes('fact'));
 await page.evaluate(()=>{document.querySelector('#ingest').scrollTop=0;document.querySelector('#ingestOutput').scrollTop=0});
 await shoot('console-ingest.png');

 // 3. Graph traversal over the links the ingestion created.
 await page.locator('nav button[data-view="graph"]').click();
 await page.locator('#graphID').fill(factRecord.id);
 await page.locator('#runGraph').click();
 await page.waitForFunction(()=>document.querySelector('#graphOutput').textContent.length>40);
 await shoot('console-graph.png');

 // 4. Memory Studio: a memory, its fields and its revision history.
 const studio=await browser.newPage({viewport:{width:1440,height:900},deviceScaleFactor:2});
 studio.on('pageerror',e=>errors.push('studio: '+e.message));
 await studio.goto(base+'/studio/');
 await studio.locator('#connect').click();
 await studio.locator('#token').fill(token);
 await studio.locator('#authForm button').first().click();
 await studio.waitForFunction(()=>document.querySelector('#list').textContent.length>10);
 await studio.locator('#list > *').first().click();
 await studio.waitForFunction(()=>document.querySelector('#editorTitle').textContent.length>0);
 if(await studio.locator('#historyBtn').isEnabled())await studio.locator('#historyBtn').click();
 await studio.waitForTimeout(300);
 await studio.screenshot({path:path.join(outArg,'studio.png')});shots.push('studio.png');
 if(errors.length)throw Error('page errors: '+errors.join('\n'));
}catch(e){console.error(e);process.exitCode=1}finally{
 if(browser)await browser.close();
 if(server){server.kill('SIGTERM');await new Promise(r=>server.once('exit',r))}
 fs.rmSync(tmp,{recursive:true,force:true});
}
if(process.exitCode!==1)for(const s of shots){
 const f=path.join(outArg,s);
 console.log(`${s}  ${(fs.statSync(f).size/1024).toFixed(0)} KB`);
}
})().catch(e=>{console.error(e);process.exitCode=1});
