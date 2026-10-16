# {{name}}

由 FORJA Python 标准栈生成的可运行骨架：FastAPI + uvicorn + pytest（Python {{opt.python_version}}）。

## 快速开始

```bash
python3 -m venv .venv && . .venv/bin/activate
pip install -r requirements-dev.txt
python3 -m pytest -q          # 单测（覆盖率 lcov: coverage/lcov.info）
python3 -m uvicorn src.app.main:app --port 8080
curl http://127.0.0.1:8080/{{opt.health_path}}
```

## 构建与运行（容器）

```bash
docker build -t {{slug}}:latest .
docker run --rm -p 8080:8080 {{slug}}:latest
```

- 健康检查：`GET /{{opt.health_path}}` → `{"status":"ok","service":"{{slug}}"}`
- pip 源默认阿里云镜像；海外环境：`docker build --build-arg PIP_INDEX_URL=https://pypi.org/simple --build-arg PIP_TRUSTED_HOST= .`

## 目录

```
src/app/main.py      # FastAPI 应用（/{{opt.health_path}}、/）
test/test_health.py  # pytest 用例
requirements*.txt    # 运行时/测试依赖
deploy/compose.yml   # 客户环境运行清单（随交付包提供）
```
