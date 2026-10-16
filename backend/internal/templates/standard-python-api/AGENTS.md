# {{name}} — 项目规范

> 本文件由 FORJA 脚手架生成，是**项目级规范唯一真相**（随仓评审、版本化）。
> 锻造时会被注入 Agent 提示词，优先级高于 FORJA 全局规则；请按项目实际修改。
> 编辑入口：FORJA「项目详情 → 规范」页签，或直接在 Git 中修改（下次锻造自动生效）。

## 技术栈
- 运行时：Python 3.12（镜像 `python:3.12-slim`）
- 框架：FastAPI + uvicorn；入口 `src/app/main.py`（`app` 实例）
- 测试：pytest + pytest-cov（`test/`；覆盖率 lcov 输出 `coverage/lcov.info`）
- 依赖：`requirements.txt`（运行时）/ `requirements-dev.txt`（测试）

## 编码约定
- 类型注解与简短 docstring；错误路径必须返回正确状态码与 JSON，禁止静默吞错
- 新增依赖：先在 Spec 说明，再更新 `requirements*.txt`；模板镜像默认走阿里云 pip 源
  （`PIP_INDEX_URL` build-arg 可覆盖为官方源）
- 密钥一律走环境变量，禁止提交任何凭据
- 禁止提交 `__pycache__/`、`.venv/`、`coverage/`（见 `.gitignore`）

## 测试与门禁
- 新增行为必须带对应 pytest 用例；提交前本地 `python3 -m pytest -q`
- CI 门禁不过视为未完成，不得进入签核

## 提交规范
- `feat|fix|docs|chore(scope): 中文简述`
