"""LangChain integration for MemoryDB: chat message history backed by the memory engine.

Each session lives in one `chat` record; LangChain-style appends become new immutable
revisions in the WAL, so `db.messages.get(pk).history()` is a free audit trail.

Install: pip install ./sdk && pip install langchain-core
"""
import json

from langchain_core.chat_history import BaseChatMessageHistory
from langchain_core.messages import BaseMessage, messages_from_dict, message_to_dict

from memorydb import MemoryDB, MemoryDBError


class MemoryDBChatMessageHistory(BaseChatMessageHistory):
    """Conversational memory stored as a versioned MemoryDB chat record."""

    def __init__(self, session_id: str, *, client: MemoryDB | None = None,
                 url: str = "http://127.0.0.1:8080", token: str | None = None,
                 source: str = "langchain", max_retries: int = 3):
        self.client = client or MemoryDB(url, token=token)
        self.session_id = session_id
        self.source = source
        self.max_retries = max_retries

    def _find(self):
        listed = self.client.request("GET", f"memories/?kind=chat&source={self.source}&limit=500")
        for hit in listed["results"]:
            if hit["record"].get("metadata", {}).get("session_id") == self.session_id:
                return hit["record"]
        return None

    @property
    def messages(self):
        record = self._find()
        if record is None:
            return []
        return messages_from_dict(json.loads(record["content"]))

    def add_messages(self, messages):
        payload_new = [message_to_dict(m) for m in messages]
        for attempt in range(self.max_retries):
            record = self._find()
            try:
                if record is None:
                    # Full-record PUT carries the version we read; a concurrent writer bumps it first,
                    # so a conflict means re-read and retry rather than lose the other append.
                    self.client.request("POST", "memories/", {
                        "kind": "chat", "content": json.dumps(payload_new),
                        "source": self.source, "layer": "raw",
                        "metadata": {"session_id": self.session_id, "method": "langchain chat history"}})
                else:
                    updated = dict(record)
                    updated["content"] = json.dumps(json.loads(record["content"]) + payload_new)
                    self.client.request("PUT", "memories/" + record["id"] + "/", updated)
                return
            except MemoryDBError:
                if attempt == self.max_retries - 1:
                    raise

    def add_message(self, message: BaseMessage):
        self.add_messages([message])

    def clear(self):
        record = self._find()
        if record is not None:
            self.client.request("DELETE", "memories/" + record["id"] + "/",
                                headers={"If-Match": str(record["version"])})
