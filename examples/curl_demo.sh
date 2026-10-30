#!/usr/bin/env bash
# End-to-end curl demo: collection -> documents -> query -> memory ingest -> search.
# Usage: TOKEN=... bash examples/curl_demo.sh [http://127.0.0.1:8080]
set -euo pipefail
URL="${2:-http://127.0.0.1:8080}"
A="Authorization: Bearer ${TOKEN:?set TOKEN to the server token}"
J='Content-Type: application/json'

echo "== health =="
curl -s -H "$A" "$URL/api/v1/health/"; echo

echo "== create collection (idempotent) =="
curl -s -H "$A" -H "$J" -X POST "$URL/api/v1/collections/" \
  -d '{"name":"demo_items","schema":{"fields":{"name":{"type":"string","required":true},"price":{"type":"number"}},"indexes":[],"unique":["name"],"strict":false}}' \
  | head -c 200; echo

echo "== add document =="
DOC=$(curl -s -H "$A" -H "$J" -X POST "$URL/api/v1/documents/demo_items/" \
  -d "{\"data\":{\"name\":\"widget-$(date +%s)\",\"price\":9.99}}")
echo "$DOC" | head -c 200; echo
ID=$(echo "$DOC" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')

echo "== query price < 20, project name =="
curl -s -H "$A" -H "$J" -X POST "$URL/api/v1/query/demo_items/" \
  -d '{"where":[{"field":"price","op":"lt","value":20}],"projection":["name"],"limit":10}'; echo

echo "== update with optimistic lock (PATCH) =="
curl -s -X PATCH -H "$A" -H "$J" "$URL/api/v1/documents/demo_items/$ID/" \
  -d '{"version":1,"set":{"price":12.5}}' | head -c 200; echo

echo "== ingest a conversation with an evidence-linked fact =="
curl -s -H "$A" -H "$J" -X POST "$URL/api/v1/ingest/" -d '{
  "source":"curl-demo",
  "messages":[{"role":"user","content":"I prefer terse answers in Hindi"}],
  "facts":[{"subject":"user","predicate":"answer_style","value":"terse Hindi",
            "quote":"I prefer terse answers in Hindi","message_index":0,"confidence":1.0}]}' \
  | python3 -c 'import json,sys; print("records:", [r["kind"] for r in json.load(sys.stdin)["results"]])'

echo "== hybrid search =="
curl -s -H "$A" -H "$J" -X POST "$URL/api/v1/search/" -d '{"text":"Hindi answers","limit":5}' \
  | python3 -c "
import json, sys
for h in json.load(sys.stdin)['results']:
    r = h['record']
    print('  %-8s %.4f  %s' % (r['kind'], h['score'], r['content'][:60]))"

echo "done."
