// Dev-only test: npm install --no-save playwright; npx playwright install chromium
const {chromium}=require('playwright');
const {spawn,execFileSync}=require('child_process');
const fs=require('fs'),os=require('os'),path=require('path'),net=require('net');
(async()=>{
const root=path.resolve(__dirname,'..'),tmp=fs.mkdtempSync(path.join(os.tmpdir(),'memorydb-ui-'));
let server,browser;
try{
 const binary=path.join(tmp,'memorydb');execFileSync('go',['build','-buildvcs=false','-o',binary,'.'],{cwd:root});
 const port=await new Promise(resolve=>{const s=net.createServer();s.listen(0,'127.0.0.1',()=>{const p=s.address().port;s.close(()=>resolve(p))})});
 const base=`http://127.0.0.1:${port}`,token='ui-test-token-only-not-production';
 server=spawn(binary,['-addr',`127.0.0.1:${port}`,'-data',path.join(tmp,'ui.wal')],{env:{...process.env,MEMORYDB_TOKEN:token},stdio:'ignore'});
 let ready=false;for(let i=0;i<100;i++){try{if((await fetch(base)).ok){ready=true;break}}catch{}await new Promise(r=>setTimeout(r,50))}if(!ready)throw Error('Server not ready');
 browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_PATH?{executablePath:process.env.CHROMIUM_PATH}:{}),args:['--no-sandbox']});
 const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(base);await page.locator('#connect').click();await page.locator('#token').fill(token);await page.locator('#authForm button').first().click();await page.waitForFunction(()=>document.querySelector('#status').textContent==='Connected');
 await page.locator('#newCollection').click();await page.locator('#name').fill('items');await page.locator('#schemaForm button').first().click();await page.waitForFunction(()=>document.querySelector('#status').textContent==='Collection created');
 await page.locator('#data').fill(JSON.stringify({name:'UI document',count:1}));await page.locator('#save').click();await page.waitForFunction(()=>document.querySelector('#status').textContent==='Document saved');
 await page.locator('#rows .row').click();await page.waitForFunction(()=>document.querySelector('#editTitle').textContent==='Edit document');await page.locator('#data').fill(JSON.stringify({name:'<script>window.attacked=true</script>',count:2}));await page.locator('#save').click();await page.waitForFunction(()=>document.querySelector('#rows').textContent.includes('window.attacked'));if(await page.evaluate(()=>window.attacked))throw Error('Stored XSS');
 await page.locator('#query').fill(JSON.stringify({where:[{field:'name',op:'eq',value:'<script>window.attacked=true</script>'}],limit:20}));await page.locator('#runQuery').click();await page.waitForFunction(()=>document.querySelector('#count').textContent.includes('equality index'));
 if(process.env.UI_SCREENSHOT)await page.screenshot({path:process.env.UI_SCREENSHOT,fullPage:true});
 await page.locator('nav button[data-view="ingestion"]').click();await page.locator('#runIngest').click();await page.waitForFunction(()=>document.querySelector('#status').textContent==='Memory ingested');
 await page.locator('#queueIngest').click();await page.waitForFunction(()=>document.querySelector('#status').textContent==='Job queued');await page.locator('nav button[data-view="jobs"]').click();await page.waitForSelector('#jobRows button');await page.locator('#jobRows button').click();await page.waitForFunction(()=>document.querySelector('#jobRows').textContent.includes('done'));
 await page.setViewportSize({width:390,height:844});if(await page.evaluate(()=>document.documentElement.scrollWidth>window.innerWidth+1))throw Error('Mobile overflow');
 await page.goto(base+'/studio/');await page.locator('#connect').click();await page.locator('#token').fill(token);await page.locator('#authForm button').first().click();await page.waitForFunction(()=>document.querySelector('#status').textContent.includes('Connected'));await page.locator('#content').fill('Browser-created fact');await page.locator('#save').click();await page.waitForFunction(()=>document.querySelector('#list').textContent.includes('Browser-created fact'));
 if(errors.length)throw Error(errors.join('\n'));console.log('Browser: connect, schema, CRUD, indexed query, stored-XSS escaping, ingest, jobs, mobile layout, memory Studio: PASS');
}finally{if(browser)await browser.close();if(server){server.kill('SIGTERM');await new Promise(r=>server.once('exit',r))}fs.rmSync(tmp,{recursive:true,force:true})}
})().catch(e=>{console.error(e);process.exitCode=1});
