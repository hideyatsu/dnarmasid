-- ================================
-- DnarMasID — Database Init
-- ================================

CREATE DATABASE IF NOT EXISTS dnarmasid_db;
USE dnarmasid_db;

-- Histori harga emas Antam
CREATE TABLE IF NOT EXISTS gold_prices (
    id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    date        DATE NOT NULL,
    gram        DECIMAL(10, 2) NOT NULL,         -- berat dalam gram (0.5, 1, 2, 5, 10, dst)
    buy_price   BIGINT NOT NULL,                 -- harga beli (Rp)
    sell_price  BIGINT NOT NULL,                 -- harga jual (Rp)
    source_url  VARCHAR(500),
    created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uq_date_gram (date, gram)
);

-- Konten hasil generate AI
CREATE TABLE IF NOT EXISTS generated_contents (
    id           BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    price_id     BIGINT UNSIGNED NOT NULL,
    platform     ENUM('instagram','twitter','facebook','threads','youtube','tiktok') NOT NULL,
    content_type ENUM('caption','thread','description','analysis') NOT NULL,
    content_text LONGTEXT NOT NULL,
    status       ENUM('pending','sent','failed') DEFAULT 'pending',
    created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (price_id) REFERENCES gold_prices(id)
);

-- File media hasil generate
CREATE TABLE IF NOT EXISTS generated_media (
    id           BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    price_id     BIGINT UNSIGNED NOT NULL,
    media_type   ENUM('image','video') NOT NULL,
    file_path    VARCHAR(500) NOT NULL,
    file_name    VARCHAR(255) NOT NULL,
    status       ENUM('pending','sent','failed') DEFAULT 'pending',
    created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (price_id) REFERENCES gold_prices(id)
);

-- Subscriber Telegram
CREATE TABLE IF NOT EXISTS subscribers (
    id             BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    chat_id        BIGINT NOT NULL UNIQUE,
    username       VARCHAR(255),
    first_name     VARCHAR(255),
    status         ENUM('active','inactive') DEFAULT 'active',
    subscribed_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

-- Log pipeline per hari
CREATE TABLE IF NOT EXISTS pipeline_logs (
    id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    date        DATE NOT NULL,
    stage       ENUM('scrape','ai_generate','media_generate','telegram_send') NOT NULL,
    status      ENUM('running','success','failed') NOT NULL,
    message     TEXT,
    started_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMP NULL
);
