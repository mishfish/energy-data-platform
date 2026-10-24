# Streamlit dashboard (ADR 0032). Dependencies come from uv.lock; uv is only needed at build time.
FROM python:3.13-slim
COPY --from=ghcr.io/astral-sh/uv:0.13.5 /uv /bin/uv
WORKDIR /app
ENV UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy UV_PYTHON_DOWNLOADS=never
COPY pyproject.toml uv.lock .python-version ./
RUN --mount=type=cache,target=/root/.cache/uv uv sync --frozen --no-dev --no-install-project
COPY .streamlit .streamlit
COPY app.py ./
COPY energy_dashboard energy_dashboard
COPY views views
RUN useradd -u 10001 -M app
USER app
EXPOSE 8501
HEALTHCHECK --interval=15s --timeout=3s --retries=5 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8501/_stcore/health')"
CMD [".venv/bin/streamlit", "run", "app.py", "--server.port=8501", "--server.address=0.0.0.0"]
