/* Smoke test against a live server. Usage: MEMORYDB_URL=memorydb://TOKEN@127.0.0.1:8080 node test.mjs */
import MemoryDB from './index.mjs';

const cs = process.env.MEMORYDB_URL || 'memorydb://token@127.0.0.1:8080';
const db = MemoryDB.fromURL(cs);

const health = await db.health();
console.log('health:', health.status, health.workspace, health.role);

const cols = await db.collections.list();
console.log('collections:', cols.map((c) => c.collection).join(', ') || '(none)');

if (!cols.some((c) => c.collection === 'node_demo')) {
  await db.collections.create('node_demo', { fields: { name: { type: 'string', required: true }, hits: { type: 'integer' } }, indexes: [], unique: [], strict: false });
}
const docs = db.documents('node_demo');
const made = await docs.create({ name: 'from-node', hits: 1 });
console.log('created doc:', made.id, 'version', made.version);
const page = await docs.query({ where: [{ field: 'name', op: 'eq', value: 'from-node' }], limit: 5 });
console.log('query plan:', page.plan, 'total:', page.total);
const patched = await docs.update(made.id, { version: made.version, set: { hits: 2 } });
console.log('patched version:', patched.version, 'hits:', patched.data.hits);
await docs.delete(made.id, patched.version);
console.log('deleted ok');
console.log('ALL NODE SDK CHECKS PASSED');
