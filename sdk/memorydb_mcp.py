#!/usr/bin/env python3
"""MCP (Model Context Protocol) server for MemoryDB over stdio JSON-RPC.

Lets any MCP-capable agent (Claude Desktop, Qoder, IDE assistants) read and write
MemoryDB memories without custom glue code. Dependency-free apart from the MemoryDB
client; point it at a server with MEMORYDB_URL=memorydb://TOKEN@host:port.

Register in an MCP client config, e.g.:
  {"mcpServers": {"memorydb": {"command": "python3", "args": ["memorydb_mcp.py"],
   "env": {"MEMORYDB_URL": "memorydb://TOKEN@127.0.0.1:8080"}}}}
"""
import json
import sys
from urllib.parse import urlencode

sys.path.insert(0, str(__import__("pathlib").Path(__file__).resolve().parent))
from memorydb import MemoryDB  # noqa: E402

PROTOCOL_VERSION = "2024-11-05"
SERVER_INFO = {"name": "memorydb", "version": "0.2.1"}

TOOL_DEFS = [
    {"name": "memorydb_health", "description": "Server status, identity and transaction sequence.",
     "inputSchema": {"type": "object", "properties": {}}},
    {"name": "memorydb_search", "description": "Hybrid BM25 + vector search over readable memories.",
     "inputSchema": {"type": "object", "properties": {
         "text": {"type": "string"}, "vector": {"type": "array", "items": {"type": "number"}},
         "kind": {"type": "string", "description": "chat|fact|summary|profile|intention|experience|skill|wiki|code|revision"},
         "source": {"type": "string"}, "limit": {"type": "integer", "default": 10}}, "required": ["text"]}},
    {"name": "memorydb_list_memories", "description": "List/search memories by kind, source and text.",
     "inputSchema": {"type": "object", "properties": {
         "kind": {"type": "string"}, "source": {"type": "string"}, "q": {"type": "string"},
         "limit": {"type": "integer", "default": 50}}}},
    {"name": "memorydb_add_memory", "description": "Store one memory record (kind + content, optional source/layer/metadata).",
     "inputSchema": {"type": "object", "properties": {
         "kind": {"type": "string", "default": "fact"}, "content": {"type": "string"},
         "source": {"type": "string", "default": "mcp"}, "layer": {"type": "string"},
         "metadata": {"type": "object", "additionalProperties": {"type": "string"}}}, "required": ["content"]}},
    {"name": "memorydb_ingest", "description": "Ingest a conversation with evidence-linked facts. Each fact quote must appear verbatim in the referenced user message.",
     "inputSchema": {"type": "object", "properties": {
         "source": {"type": "string", "default": "mcp"},
         "messages": {"type": "array", "items": {"type": "object", "properties": {
             "role": {"type": "string"}, "content": {"type": "string"}}, "required": ["role", "content"]}},
         "facts": {"type": "array", "items": {"type": "object", "properties": {
             "subject": {"type": "string"}, "predicate": {"type": "string"}, "value": {"type": "string"},
             "quote": {"type": "string"}, "message_index": {"type": "integer"},
             "confidence": {"type": "number", "default": 1.0}},
             "required": ["subject", "predicate", "value", "quote", "message_index"]}}},
         "required": ["messages"]}},
    {"name": "memorydb_create_collection", "description": "Create a document collection with a JSON schema (admin role required).",
     "inputSchema": {"type": "object", "properties": {
         "name": {"type": "string"},
         "schema": {"type": "object", "properties": {
             "fields": {"type": "object"}, "unique": {"type": "array", "items": {"type": "string"}},
             "indexes": {"type": "array", "items": {"type": "string"}}, "strict": {"type": "boolean"}}}},
         "required": ["name", "schema"]}},
    {"name": "memorydb_query_documents", "description": "Query a collection: where[op eq|ne|gt|gte|lt|lte|in|contains|prefix|exists], order_by, descending, projection, limit.",
     "inputSchema": {"type": "object", "properties": {
         "collection": {"type": "string"},
         "where": {"type": "array", "items": {"type": "object", "properties": {
             "field": {"type": "string"}, "op": {"type": "string"}, "value": {}}}},
         "order_by": {"type": "string"}, "descending": {"type": "boolean"},
         "projection": {"type": "array", "items": {"type": "string"}},
         "limit": {"type": "integer", "default": 50}}, "required": ["collection"]}},
]


def handle(db, method, params):
    if method == "memorydb_health":
        return db.request("GET", "health/")
    if method == "memorydb_search":
        return db.objects.search(text=params.get("text", ""), vector=params.get("vector"),
                         kind=params.get("kind", ""), source=params.get("source", ""),
                         limit=params.get("limit", 10))
    if method == "memorydb_list_memories":
        return db.request("GET", "memories/?" + urlencode({"kind": params.get("kind", ""),
                "source": params.get("source", ""), "q": params.get("q", ""), "limit": params.get("limit", 50)}))
    if method == "memorydb_add_memory":
        body = {"kind": params.get("kind", "fact"), "content": params["content"],
                "source": params.get("source", "mcp")}
        if params.get("layer"):
            body["layer"] = params["layer"]
        if params.get("metadata"):
            body["metadata"] = params["metadata"]
        return db.request("POST", "memories/", body)
    if method == "memorydb_ingest":
        return db.ingest(params["messages"], facts=params.get("facts"), source=params.get("source", "mcp"))
    if method == "memorydb_create_collection":
        schema = params["schema"]
        return db.collections.create(params["name"], fields=schema.get("fields"),
                                     unique=schema.get("unique"), indexes=schema.get("indexes"),
                                     strict=schema.get("strict", False))
    if method == "memorydb_query_documents":
        q = {k: v for k, v in params.items() if k != "collection"}
        return db.request("POST", "query/" + params["collection"] + "/", q)
    raise ValueError("unknown tool " + method)


def serve():
    import os
    env_url = os.environ.get("MEMORYDB_URL", "")
    if env_url.startswith("memorydb"):
        db = MemoryDB.from_url(env_url)
    else:
        db = MemoryDB(env_url or "http://127.0.0.1:8080", token=os.environ.get("MEMORYDB_TOKEN"))
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except json.JSONDecodeError:
            continue
        method, params, msg_id = msg.get("method"), msg.get("params", {}), msg.get("id")
        if method == "initialize":
            reply(msg_id, {"protocolVersion": PROTOCOL_VERSION, "capabilities": {"tools": {}}, "serverInfo": SERVER_INFO})
        elif method == "notifications/initialized" or msg_id is None:
            continue
        elif method == "tools/list":
            reply(msg_id, {"tools": TOOL_DEFS})
        elif method == "tools/call":
            tool, arguments = params.get("name"), params.get("arguments", {})
            try:
                result = handle(db, tool, arguments)
                reply(msg_id, {"content": [{"type": "text", "text": json.dumps(result, default=str)}]})
            except Exception as exc:  # MCP convention: tool errors are results, not protocol faults
                reply(msg_id, {"content": [{"type": "text", "text": str(exc)}], "isError": True})
        elif method == "ping":
            reply(msg_id, {})
        else:
            reply(msg_id, None, error={"code": -32601, "message": "method not found: " + str(method)})


def reply(msg_id, result, error=None):
    out = {"jsonrpc": "2.0", "id": msg_id}
    if error:
        out["error"] = error
    else:
        out["result"] = result
    sys.stdout.write(json.dumps(out) + "\n")
    sys.stdout.flush()


if __name__ == "__main__":
    serve()
