from fastapi import FastAPI
from .settings import settings
app = FastAPI(title="Acme API")
@app.get("/healthz")
def healthz(): return {"ok": True}
