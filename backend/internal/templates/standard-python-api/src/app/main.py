"""{{name}} API 入口（FastAPI 标准骨架）。"""
from fastapi import FastAPI

app = FastAPI(title="{{name}}"{{#opt.disable_redoc}}, redoc_url=None{{/opt.disable_redoc}})


@app.get("/{{opt.health_path}}")
def health() -> dict:
    """健康检查（交付 compose healthcheck 与客户验收入口）。"""
    return {"status": "ok", "service": "{{slug}}"}


@app.get("/")
def index() -> dict:
    return {"message": "{{name}} running"}
