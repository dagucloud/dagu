# The git server of the agent worker demo: Python and git, nothing else.
FROM debian:stable-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends git python3 ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY server.py /app/server.py
ENV HOME=/tmp
ENTRYPOINT ["python3", "/app/server.py"]
