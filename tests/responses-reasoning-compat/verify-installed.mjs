import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import { pathToFileURL } from 'node:url';
import net from 'node:net';

const root = process.env.DSH_INSTALL_ROOT || 'D:/Software/AI/DSH-Desktop-x64-Portable/App/resources/app.asar/dsh';
const base = root + '/node_modules/';
const load = (p) => import(pathToFileURL(base + p).href);
const packages = ['@earendil-works/pi-ai', '@deepseek-ai/dsh-llm-pi-ai', '@deepseek-ai/dsh-llm'];
const runtimeVersion = JSON.parse(await fs.readFile(root+'/package.json','utf8')).version;
const versions = Object.fromEntries(await Promise.all(packages.map(async (p) => [p, JSON.parse(await fs.readFile(base+p+'/package.json','utf8')).version])));
let socketAttempts = 0;
const originalConnect = net.Socket.prototype.connect;
net.Socket.prototype.connect = function() { socketAttempts++; throw new Error('Network forbidden by offline acceptance'); };
const originalFetch = globalThis.fetch;
let currentWire = '';
let requests = 0;
globalThis.fetch = async (url, options) => {
  assert.equal(String(url), 'https://offline.invalid/v1/responses');
  assert.equal(options.method, 'POST');
  requests++;
  const bytes = new TextEncoder().encode(currentWire);
  let offset = 0;
  return new Response(new ReadableStream({pull(controller) {
    if (offset === bytes.length) return controller.close();
    const end = Math.min(offset + 7, bytes.length);
    controller.enqueue(bytes.slice(offset,end)); offset=end;
  }}), {status:200,headers:{'content-type':'text/event-stream'}});
};
const results = [];
try {
  const {PiAiAdapter} = await load('@deepseek-ai/dsh-llm-pi-ai/lib/index.js');
  const {AssistantStreamAccumulator, BlockAssembler, expandAssistantStream} = await load('@deepseek-ai/dsh-llm/lib/index.js');
  const {createProvider} = await load('@earendil-works/pi-ai/dist/models.js');
  const api = await load('@earendil-works/pi-ai/dist/api/openai-responses.js');
  const {envApiKeyAuth} = await load('@earendil-works/pi-ai/dist/auth/helpers.js');
  const model = {id:'m',name:'Offline fixture',provider:'compat-offline',api:'openai-responses',baseUrl:'https://offline.invalid/v1',reasoning:true,input:['text'],contextWindow:32000,maxTokens:1024,cost:{input:0,output:0,cacheRead:0,cacheWrite:0}};
  const piProvider = createProvider({id:'compat-offline',name:'Offline fixture',baseUrl:model.baseUrl,auth:{apiKey:envApiKeyAuth('Offline fixture',[])},models:[model],api});
  const profiles = new Map([['compat-offline',{piProvider,modelErrors:new Map(),configuredMaxTokens:new Map(),streamIdleTimeoutMs:10000}]]);
  const adapter = new PiAiAdapter({profiles:()=>profiles,resolveApiKey:async()=> 'offline-not-a-real-key'});
  const fixtures = JSON.parse(await fs.readFile(new URL('./fixtures.json',import.meta.url),'utf8'));
  const parse = (raw) => JSON.parse(raw.split(String.fromCharCode(10)).find(x=>x.startsWith('data: ')).slice(6));
  const encode = (e) => ['event: '+e.type,'data: '+JSON.stringify(e),'',''].join(String.fromCharCode(10));
  const standard = [
    {type:'response.created',response:{id:'standard'}},
    {type:'response.output_item.added',output_index:0,item:{id:'r',type:'reasoning',summary:[]}},
    {type:'response.reasoning_summary_part.added',output_index:0,summary_index:0,part:{type:'summary_text',text:''}},
    {type:'response.reasoning_summary_text.delta',output_index:0,summary_index:0,delta:'思考 '},
    {type:'response.reasoning_summary_text.delta',output_index:0,summary_index:0,delta:'😀 done'},
    {type:'response.reasoning_summary_text.done',output_index:0,summary_index:0,text:'思考 😀 done'},
    {type:'response.reasoning_summary_part.done',output_index:0,summary_index:0,part:{type:'summary_text',text:'思考 😀 done'}},
    {type:'response.output_item.done',output_index:0,item:{id:'r',type:'reasoning',summary:[{type:'summary_text',text:'思考 😀 done'}]}},
    {type:'response.completed',response:{id:'standard',status:'completed',output:[]}}
  ].map(encode);
  const cases = Object.entries(fixtures).map(([name,wire])=>({name,wire,expectReasoning:!name.endsWith('no_reasoning'),follow:name.split('/')[1]}));
  cases.push({name:'standard/passthrough',wire:standard,expectReasoning:true,expected:'思考 😀 done'});
  for (const source of ['chat','claude','gemini']) {
    const events = fixtures[source+'/text'];
    const cut = events.findIndex(x=>parse(x).type==='response.reasoning_summary_text.done');
    const prefix = events.slice(0,cut);
    cases.push({name:source+'/early_eof',wire:prefix,expectReasoning:true,error:true});
    for (const type of ['response.incomplete','response.failed']) {
      cases.push({name:source+'/'+type,wire:[...prefix,encode({type,response:{id:'error',status:type.split('.')[1],incomplete_details:{reason:'upstream_error'},error:{code:'upstream_error',message:'offline synthetic interruption'},output:[]}})],expectReasoning:true,error:true});
    }
  }
  for (const test of cases) {
    const observed = [];
    const acc = new AssistantStreamAccumulator();
    const assembly = new BlockAssembler();
    try {
      currentWire = test.wire.join('');
      let i=0;
      for await (const chunk of adapter.stream({provider:'compat-offline',model:'m',messages:[],maxTokens:100})) {
        observed.push(chunk);
        assembly.push(acc.push({time:++i,chunk}).chunk);
      }
      const record = JSON.parse(JSON.stringify(acc.snapshot()));
      const restored = [...expandAssistantStream(record)];
      assert.deepEqual(restored.map(x=>x.chunk),observed,'durable accumulator round-trip');
      const replayAssembly = new BlockAssembler();
      for (const timed of restored) replayAssembly.push(timed.chunk);
      assert.deepEqual(replayAssembly.blocks(),assembly.blocks(),'assembly replay');
      const deltas = observed.filter(x=>x.type==='reasoning-delta').map(x=>x.text);
      assert(deltas.every(x=>typeof x==='string' && !x.includes('undefined')),'typed lossless reasoning deltas');
      const expected = test.expected || 'first second';
      if (test.expectReasoning) {
        assert.equal(deltas.join('').replace(new RegExp(String.fromCharCode(10,10)+'$'),''),expected,'raw delta concatenation');
        assert.equal(assembly.blocks().find(x=>x.type==='reasoning')?.text.replace(new RegExp(String.fromCharCode(10,10)+'$'),''),expected,'assembled reasoning');
      } else assert.equal(deltas.length,0,'no reasoning');
      if(test.follow==='text'||test.follow==='no_reasoning') assert.equal(assembly.blocks().find(x=>x.type==='text')?.text,'answer');
      if(test.follow==='tool') {
        const tool=assembly.blocks().find(x=>x.type==='tool-call');
        assert.equal(tool?.name,'lookup');
        assert.deepEqual(JSON.parse(tool.arguments),{});
      }
      const finish=observed.filter(x=>x.type==='finish');
      assert.equal(finish.length,1,'exactly one terminal');
      if (test.follow === 'tool') assert.equal(finish[0].reason.kind,'tool-calls','tool terminal must not become stop');
      if (test.error) assert.equal(finish[0].reason.kind,'error','interruption must not become success');
      else assert(['stop','tool-calls'].includes(finish[0].reason.kind),'successful terminal: '+JSON.stringify(finish[0]));
      results.push({name:test.name,status:'passed',deltas,blocks:assembly.blocks(),finish:finish[0].reason,record});
    } catch(error) {results.push({name:test.name,status:'failed',error:String(error),observed,blocks:assembly.blocks(),record:acc.snapshot()});}
  }
  assert.equal(socketAttempts,0);
} finally { globalThis.fetch=originalFetch; net.Socket.prototype.connect=originalConnect; }
const report={runtimeVersion,nodeVersion:process.version,versions,entries:{adapter:base+'@deepseek-ai/dsh-llm-pi-ai/lib/index.js',parser:base+'@earendil-works/pi-ai/dist/api/openai-responses.js',accumulator:base+'@deepseek-ai/dsh-llm/lib/index.js'},network:{requestsServedInMemory:requests,socketAttempts},results};
await fs.writeFile(new URL('./installed-results.json',import.meta.url),JSON.stringify(report,null,2));
for(const r of results) console.log(r.status.toUpperCase(),r.name,r.error||JSON.stringify(r.finish));
assert(results.every(r=>r.status==='passed'),'installed acceptance failures; see installed-results.json');
