"""LangChain chat history on MemoryDB.

Setup: pip install superb-memory memorydb-langchain (or -e sdk/langchain from a clone)
Run:   MEMORYDB_URL=memorydb://TOKEN@127.0.0.1:8080 python3 examples/langchain_demo.py
"""
import os
import sys

_here = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(_here, "..", "sdk"))
sys.path.insert(0, os.path.join(_here, "..", "sdk", "langchain"))

from langchain_core.messages import AIMessage, HumanMessage
from memorydb import MemoryDB
from memorydb_langchain import MemoryDBChatMessageHistory

db = MemoryDB()
history = MemoryDBChatMessageHistory("demo-session", client=db)

history.add_message(HumanMessage("What is the capital of Rajasthan?"))
history.add_message(AIMessage("Jaipur."))

print("messages:", [(m.type, m.content) for m in history.messages])

record = history._find()
print("revision trail (free audit log):", [r["version"] for r in db.objects.history(pk=record["id"])])

history.clear()
