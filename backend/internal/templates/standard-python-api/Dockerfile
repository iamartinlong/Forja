# Python 标准栈镜像（FastAPI + uvicorn）。
# pip 源默认阿里云镜像（本环境 PyPI 直连超时实录 2026-09-16）；海外环境可用
#   docker build --build-arg PIP_INDEX_URL=https://pypi.org/simple --build-arg PIP_TRUSTED_HOST=
# 覆盖为官方源。
FROM python:{{opt.python_version}}-slim

ARG PIP_INDEX_URL=https://mirrors.aliyun.com/pypi/simple/
ARG PIP_TRUSTED_HOST=mirrors.aliyun.com

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PORT=8080

WORKDIR /app

COPY requirements.txt ./
RUN pip install -r requirements.txt -i "$PIP_INDEX_URL" --trusted-host "$PIP_TRUSTED_HOST"

COPY . .

# 非 root 运行（python slim 无内置用户，显式创建）
RUN useradd --create-home --uid 10001 appuser
USER appuser

EXPOSE 8080

CMD ["python3", "-m", "uvicorn", "src.app.main:app", "--host", "0.0.0.0", "--port", "8080"]
