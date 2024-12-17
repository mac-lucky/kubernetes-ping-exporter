# Build stage
FROM python:3.12-slim as builder

WORKDIR /app
COPY requirements.txt .

RUN pip install --no-cache-dir --user -r requirements.txt

# Final stage
FROM python:3.12-slim

WORKDIR /app
COPY --from=builder /root/.local /root/.local
COPY ping-exporter.py .

# Install fping and clean up
RUN apt-get update \
    && apt-get install -y --no-install-recommends fping \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

ENV PATH=/root/.local/bin:$PATH
EXPOSE 9107

CMD ["python", "-u", "ping-exporter.py"]