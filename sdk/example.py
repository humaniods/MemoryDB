import os
from memorydb import MemoryDB

Memory = MemoryDB(token=os.environ["MEMORYDB_TOKEN"])
record = Memory.objects.create(kind="fact", content="Sanyam prefers Hindi-English explanations", source="django", layer="fact")
print(Memory.objects.filter(kind="fact", q="Hindi"))
updated = Memory.objects.update(pk=record["id"], version=record["version"], content="Sanyam prefers Hinglish and concrete examples")
print(Memory.objects.history(pk=updated["id"]))
