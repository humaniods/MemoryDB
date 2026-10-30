/* Node quickstart — run from a clone: MEMORYDB_URL=memorydb://TOKEN@127.0.0.1:8080 node examples/node_quickstart.mjs */
import { MemoryDB } from '../sdk/node/index.mjs';

const db = new MemoryDB(process.env.MEMORYDB_URL); // connection string or {url, token}

console.log('health:', (await db.health()).status);

const cols = await db.collections.list();
if (!cols.some((c) => c.collection === 'node_items')) {
  await db.collections.create('node_items', {
    fields: { name: { type: 'string', required: true }, stock: { type: 'integer' } },
  });
}

const items = db.documents('node_items');
const doc = await items.create({ name: 'keyboard', stock: 12 });
console.log('created:', doc.id, 'v' + doc.version);

const patched = await items.update(doc.id, { version: doc.version, set: { stock: 11 } });
console.log('stock after sale:', patched.data.stock);

const fact = await db.memories.create({ kind: 'fact', content: 'user buys keyboards', source: 'node-demo' });
const hits = await db.search({ text: 'keyboards', limit: 5 });
console.log('search hits:', hits.results.map((h) => h.record.kind));

await db.memories.delete(fact.id, fact.version);
console.log('node quickstart OK');
