from fastapi.testclient import TestClient

from src.app.main import app

client = TestClient(app)


def test_health() -> None:
    res = client.get("/{{opt.health_path}}")
    assert res.status_code == 200
    body = res.json()
    assert body["status"] == "ok"
    assert body["service"]


def test_index() -> None:
    res = client.get("/")
    assert res.status_code == 200
    assert "running" in res.json()["message"]


def test_unknown_path_returns_404() -> None:
    assert client.get("/nope").status_code == 404
