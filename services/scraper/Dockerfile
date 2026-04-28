FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN apk add --no-cache git
RUN go mod download

COPY shared/ ./shared/
COPY services/storage/ ./services/storage/
COPY services/scraper/ ./services/scraper/

RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o /bin/scraper ./services/scraper

FROM ubuntu:22.04

# Set noninteractive mode for apt
ENV DEBIAN_FRONTEND=noninteractive
ENV TZ=Asia/Jakarta

# Install Chrome and dependencies
RUN echo 'tzdata tzdata/Areas select Etc' | debconf-set-selections && \
    echo 'tzdata tzdata/Zones/etc select Asia' | debconf-set-selections && \
    echo 'tzdata tzdata/Zones/Asia select Jakarta' | debconf-set-selections && \
    apt-get update && apt-get install -y \
    wget \
    gnupg \
    ca-certificates \
    fonts-liberation \
    libasound2 \
    libatk-bridge2.0-0 \
    libatk1.0-0 \
    libcups2 \
    libdbus-1-3 \
    libdrm2 \
    libgbm1 \
    libgtk-3-0 \
    libnspr4 \
    libnss3 \
    libx11-6 \
    libx11-xcb1 \
    libxcb1 \
    libxcomposite1 \
    libxdamage1 \
    libxext6 \
    libxfixes3 \
    libxkbcommon0 \
    libxrandr2 \
    xdg-utils \
    tzdata \
    unzip \
    && wget -q -O /usr/local/bin/chromedriver https://storage.googleapis.com/chrome-for-testing-public/124.0.6367.91/linux64/chromedriver-linux64.zip \
    && unzip /usr/local/bin/chromedriver -d /usr/local/bin/ \
    && rm /usr/local/bin/chromedriver \
    && wget -q -O /usr/local/bin/chrome https://storage.googleapis.com/chrome-for-testing-public/124.0.6367.91/linux64/chrome-linux64.zip \
    && unzip /usr/local/bin/chrome -d /usr/local/bin/ \
    && rm /usr/local/bin/chrome \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Create symlinks for chromedp
RUN ln -sf /usr/local/bin/chromedriver-linux64/chromedriver /usr/local/bin/chromedriver \
    && ln -sf /usr/local/bin/chrome-linux64/chrome /usr/local/bin/chrome

ENV CHROME_BIN=/usr/local/bin/chrome
ENV CHROMEDRIVER_PATH=/usr/local/bin/chromedriver

WORKDIR /app

COPY --from=builder /bin/scraper .

CMD ["./scraper"]
