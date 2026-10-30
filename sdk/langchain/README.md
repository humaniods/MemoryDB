# memorydb-langchain

LangChain chat message history backed by
[MemoryDB](https://github.com/humaniods/MemoryDB), so agent conversations survive process
restarts and stay searchable alongside the facts you ingest from them.

```bash
pip install memorydb-langchain
export MEMORYDB_URL=memorydb://TOKEN@127.0.0.1:8080
```

```python
from memorydb import MemoryDB
from memorydb_langchain import MemoryDBChatMessageHistory
from langchain_core.messages import HumanMessage, AIMessage

history = MemoryDBChatMessageHistory(session_id="user-42", client=MemoryDB())
history.add_message(HumanMessage("What is the capital of Rajasthan?"))
history.add_message(AIMessage("Jaipur."))
history.messages          # [HumanMessage, AIMessage]
history.clear()
```

Every append writes a new immutable revision of one record rather than rewriting a table, so the
server keeps the full trail:

```python
db = MemoryDB()
db.objects.history(pk=history.record_id)["results"]   # versions 1..n, a free audit log
```

Use one principal (token) per deployed product so sessions of different products land in
different workspaces, and give each end-user their own `session_id`. Search the whole history
with `db.objects.search("Jaipur")`, including hybrid BM25 + vector ranking.
