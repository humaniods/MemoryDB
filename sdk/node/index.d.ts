export declare class MemoryDBError extends Error {
  status: number;
  constructor(status: number, message: string);
}

export interface MemoryDBOptions {
  url?: string;
  token?: string;
  timeout?: number;
}

export interface Record {
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
  search(query: { text?: string; vector?: number[]; kind?: string; source?: string; embedding_model?: string; agent?: string; limit?: number }): Promise<{ results: { record: Record; score: number }[] }>;
  ingest(payload: { source: string; messages: { role: string; content: string }[]; facts?: unknown[]; extract?: boolean; context?: string; tools?: unknown[] }): Promise<{ results: Record[] }>;
  queueIngest(payload: object): Promise<Record>;
  transaction(changes: { record: object; expected: number }[], idempotencyKey?: string): Promise<{ results: Record[] }>;
  memories: {
    create(record: object): Promise<Record>;
    list(opts?: { kind?: string; source?: string; q?: string; limit?: number }): Promise<{ results: Record[] }>;
    get(pk: string): Promise<Record>;
    history(pk: string): Promise<{ results: Record[] }>;
    delete(pk: string, version: number): Promise<Record>;
  };
  collections: {
    list(): Promise<Record[]>;
    create(name: string, schema: object): Promise<Record>;
  };
  documents(name: string): {
    create(data: object, idempotencyKey?: string): Promise<Record>;
    get(pk: string): Promise<Record>;
    query(q?: object): Promise<{ results: Record[]; total: number; plan: string; next_cursor?: string }>;
    update(pk: string, opts: { version?: number; set?: object; unset?: string[] }): Promise<Record>;
    delete(pk: string, version: number): Promise<Record>;
  };
}

export default MemoryDB;
