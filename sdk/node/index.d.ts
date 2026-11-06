export declare class MemoryDBError extends Error {
  status: number;
  constructor(status: number, message: string);
}

export interface MemoryDBOptions {
  url?: string;
  token?: string;
  timeout?: number;
}

/**
 * One stored record. Named MemoryDBRecord rather than Record on purpose: an exported
 * `interface Record` would shadow TypeScript's own `Record<KeyType, ValueType>`, and the
 * published 0.2.6 declarations did exactly that - every TypeScript project that installed
 * them failed to compile, because the file then used `Record<string, string>` meaning the
 * interface above, which takes no type arguments.
 */
export interface MemoryDBRecord {
  id: string;
  kind: string;
  content: string;
  data?: Record<string, unknown> | null;
  collection?: string;
  version: number;
  user: string;
  workspace: string;
  source: string;
  layer?: string;
  parent?: string;
  metadata?: Record<string, string>;
  created: string;
  deleted: boolean;
}

export declare class MemoryDB {
  constructor(urlOrOptions?: string | MemoryDBOptions, maybeOptions?: MemoryDBOptions);
  static fromURL(connectionString: string): MemoryDB;
  request(method: string, path: string, body?: unknown, headers?: Record<string, string>): Promise<any>;
  health(): Promise<any>;
  search(query: { text?: string; vector?: number[]; kind?: string; source?: string; embedding_model?: string; agent?: string; limit?: number }): Promise<{ results: { record: MemoryDBRecord; score: number }[] }>;
  ingest(payload: { source: string; messages: { role: string; content: string }[]; facts?: unknown[]; extract?: boolean; context?: string; tools?: unknown[] }): Promise<{ results: MemoryDBRecord[] }>;
  queueIngest(payload: object): Promise<MemoryDBRecord>;
  transaction(changes: { record: object; expected: number }[], idempotencyKey?: string): Promise<{ results: MemoryDBRecord[] }>;
  memories: {
    create(record: object): Promise<MemoryDBRecord>;
    list(opts?: { kind?: string; source?: string; q?: string; limit?: number }): Promise<{ results: { record: MemoryDBRecord; score: number }[] }>;
    get(pk: string): Promise<MemoryDBRecord>;
    history(pk: string): Promise<{ results: MemoryDBRecord[] }>;
    delete(pk: string, version: number): Promise<MemoryDBRecord>;
  };
  collections: {
    list(): Promise<MemoryDBRecord[]>;
    create(name: string, schema: object): Promise<MemoryDBRecord>;
  };
  documents(name: string): {
    create(data: object, idempotencyKey?: string): Promise<MemoryDBRecord>;
    get(pk: string): Promise<MemoryDBRecord>;
    query(q?: object): Promise<{ results: MemoryDBRecord[]; total: number; plan: string; next_cursor?: string }>;
    update(pk: string, opts: { version?: number; set?: object; unset?: string[] }): Promise<MemoryDBRecord>;
    delete(pk: string, version: number): Promise<MemoryDBRecord>;
  };
}

export default MemoryDB;
