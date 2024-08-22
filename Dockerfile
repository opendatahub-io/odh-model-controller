FROM --platform=$TARGETPLATFORM ubuntu:20.04

ENV DEBIAN_FRONTEND=noninteractive
ENV LANG=C.UTF-8

RUN apt-get update && \
    apt-get install -y \
    build-essential \
    curl \
    git \
    ca-certificates && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY . .

RUN make build

EXPOSE 8080

ENTRYPOINT ["./myapp"]

CMD ["--help"]

