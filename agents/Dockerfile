FROM python:3.12-slim
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PORT=8080
WORKDIR /app
COPY . /app
RUN pip install --no-cache-dir . && useradd --create-home --uid 10001 agent
WORKDIR /srv
USER agent
CMD ["sh", "-c", "exec uvicorn coslash_agents.app:app --host 0.0.0.0 --port ${PORT}"]
