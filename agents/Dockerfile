FROM python:3.12-slim@sha256:b18b261e1b4974b4f321451ad5251ded85abb22c2b8b262da527dda1d031ab30
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PORT=8080
WORKDIR /app
COPY . /app
RUN pip install --no-cache-dir . && useradd --create-home --uid 10001 agent
WORKDIR /srv
USER agent
CMD ["sh", "-c", "exec uvicorn coslash_agents.app:app --host 0.0.0.0 --port ${PORT}"]
