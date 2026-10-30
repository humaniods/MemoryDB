/**
 * Dependency-free MemoryDB client for Node 18+ (native fetch).
 * Accepts either ({url, token}) or a memorydb://TOKEN@host:port connection string,
 * matching the Python/Go clients and the MEMORYDB_URL environment variable.
 */
const DEFAULT_PORT = 8080;

export function parseConnectionString(cs) {
  const u = new URL(cs);
  if (u.protocol !== 'memorydb:' && u.protocol !== 'memorydbs:') {
    throw new Error('connection string must start with memorydb:// or memorydbs://');
  }
  const token = decodeURIComponent(u.username);
  if (!token) throw new Error('connection string must carry the token: memorydb://TOKEN@host:port');
  const scheme = u.protocol === 'memorydbs:' ? 'https' : 'http';
  const host = u.hostname || '127.0.0.1';
  const port = u.port || String(DEFAULT_PORT);
  return { url: `${scheme}://${host}:${port}`, token };
}

export class MemoryDBError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

export class MemoryDB {
  constructor(urlOrOptions = 'http://127.0.0.1:8080', maybeOptions = {}) {
    let url, token, timeout = 35000;
    if (typeof urlOrOptions === 'string' && urlOrOptions.startsWith('memorydb')) {
      ({ url, token } = parseConnectionString(urlOrOptions));
    } else if (typeof urlOrOptions === 'object') {
      ({ url, token, timeout = 35000 } = urlOrOptions);
    } else {
      url = urlOrOptions;
      token = maybeOptions.token;
      timeout = maybeOptions.timeout ?? timeout;
    }
    if (!token) token = process.env.MEMORYDB_TOKEN;
    if (!url) url = process.env.MEMORYDB_URL;
    if (url && url.startsWith('memorydb')) ({ url, token } = parseConnectionString(url));
    if (!token) throw new Error('pass token=, a memorydb:// connection string, or set MEMORYDB_TOKEN');
    this.base = url.replace(/\/+$/, '') + '/api/v1/';
    this.token = token;
    this.timeout = timeout;
    this.documents = (name) => new DocumentManager(this, name);
    this.memories = new MemoryManager(this);
    this.collections = new CollectionManager(this);
  }

  static fromURL(cs) {
    return new MemoryDB(cs);
  }

  async request(method, path, body, headers = {}) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeout);
    try {
      const res = await fetch(this.base + path, {
        method,
        headers: { Authorization: `Bearer ${this.token}`, 'Content-Type': 'application/json', ...headers },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: controller.signal,
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new MemoryDBError(res.status, data.error || `HTTP ${res.status}`);
      return data;
    } finally {
      clearTimeout(timer);
    }
  }

  health() { return this.request('GET', 'health/'); }
  search(query) { return this.request('POST', 'search/', { limit: 10, ...query }); }
  ingest(payload) { return this.request('POST', 'ingest/', payload); }
  queueIngest(payload) { return this.request('POST', 'jobs/', payload); }
  transaction(changes, idempotencyKey) {
    return this.request('POST', 'transactions/', { changes }, idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : {});
  }
}

class MemoryManager {
  constructor(client) { this.client = client; }
  create(record) { return this.client.request('POST', 'memories/', record); }
  list({ kind = '', source = '', q = '', limit = 50 } = {}) {
    const params = new URLSearchParams({ kind, source, q, limit: String(limit) });
    return this.client.request('GET', 'memories/?' + params);
  }
  get(pk) { return this.client.request('GET', `memories/${encodeURIComponent(pk)}/`); }
  history(pk) { return this.client.request('GET', `memories/${encodeURIComponent(pk)}/history/`); }
  delete(pk, version) {
    return this.client.request('DELETE', `memories/${encodeURIComponent(pk)}/`, undefined, { 'If-Match': String(version) });
  }
}

class CollectionManager {
  constructor(client) { this.client = client; }
  list() { return this.client.request('GET', 'collections/').then((d) => d.results); }
  create(name, schema) { return this.client.request('POST', 'collections/', { name, schema }); }
}

class DocumentManager {
  constructor(client, name) {
    if (!/^[A-Za-z][A-Za-z0-9_-]{0,63}$/.test(name)) throw new Error('invalid collection name');
    this.client = client;
    this.path = `documents/${encodeURIComponent(name)}/`;
  }
  create(data, idempotencyKey) {
    return this.client.request('POST', this.path, { data }, idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : {});
  }
  get(pk) { return this.client.request('GET', this.path + encodeURIComponent(pk) + '/'); }
  query(q = {}) { return this.client.request('POST', 'query/' + this.path.slice('documents/'.length), q); }
  update(pk, { version, set = {}, unset = [] }) {
    return this.client.request('PATCH', this.path + encodeURIComponent(pk) + '/', { version, set, unset });
  }
  delete(pk, version) {
    return this.client.request('DELETE', this.path + encodeURIComponent(pk) + '/', undefined, { 'If-Match': String(version) });
  }
}

export default MemoryDB;
