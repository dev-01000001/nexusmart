-- NexusMart Core Backend: per-service isolated PostgreSQL databases
-- No service may query another service's database directly.

CREATE DATABASE user_db;
CREATE DATABASE product_db;
CREATE DATABASE promotion_db;
CREATE DATABASE order_db;
CREATE DATABASE payment_db;
CREATE DATABASE shipping_db;
CREATE DATABASE notification_db;
