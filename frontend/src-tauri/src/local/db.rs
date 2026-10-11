use rusqlite::Connection;
use std::path::Path;

use super::auth;

/// Default timestamp expression producing RFC3339-ish UTC strings
/// ("2026-09-24T10:15:30.123Z"), matching how Go serialized time.Time to JSON.
pub const TS_DEFAULT: &str = "(strftime('%Y-%m-%dT%H:%M:%fZ','now'))";

pub fn now_ts() -> String {
    chrono::Utc::now().format("%Y-%m-%dT%H:%M:%S%.6fZ").to_string()
}

pub fn init_db(path: &Path) -> Result<Connection, rusqlite::Error> {
    if let Some(parent) = path.parent() {
        let _ = std::fs::create_dir_all(parent);
    }
    let conn = Connection::open(path)?;
    conn.pragma_update(None, "foreign_keys", "ON")?;
    conn.pragma_update(None, "journal_mode", "WAL")?;
    run_migrations(&conn)?;
    run_seeds(&conn)?;
    Ok(conn)
}

fn run_migrations(conn: &Connection) -> Result<(), rusqlite::Error> {
    let ts = TS_DEFAULT;
    let ddl = format!(
        r#"
CREATE TABLE IF NOT EXISTS stores (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    branch TEXT,
    location TEXT,
    gstin TEXT,
    fssai_no TEXT,
    phone TEXT,
    printer_name TEXT,
    printer_vendor_id TEXT,
    printer_product_id TEXT,
    invoice_size TEXT DEFAULT '3inch',
    kot_print_enabled INTEGER DEFAULT 1,
    remote_billing_enabled INTEGER DEFAULT 0,
    logo_url TEXT,
    theme_color TEXT,
    tax_enabled INTEGER DEFAULT 1,
    default_tax_percent REAL DEFAULT 0,
    is_active INTEGER DEFAULT 1,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT
);

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password TEXT NOT NULL,
    name TEXT NOT NULL,
    email TEXT,
    role TEXT NOT NULL CHECK (role IN ('superadmin', 'business_owner', 'business_admin', 'staff')),
    store_id TEXT REFERENCES stores(id) ON DELETE SET NULL,
    is_active INTEGER DEFAULT 1,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT
);

CREATE TABLE IF NOT EXISTS user_stores (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT REFERENCES users(id) ON DELETE CASCADE,
    store_id TEXT REFERENCES stores(id) ON DELETE CASCADE,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT,
    UNIQUE(user_id, store_id)
);

CREATE TABLE IF NOT EXISTS categories (
    id TEXT PRIMARY KEY,
    store_id TEXT REFERENCES stores(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    is_active INTEGER DEFAULT 1,
    enabled INTEGER DEFAULT 1,
    is_favourite INTEGER DEFAULT 0,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT
);

CREATE TABLE IF NOT EXISTS items (
    id TEXT PRIMARY KEY,
    store_id TEXT REFERENCES stores(id) ON DELETE CASCADE,
    category_id TEXT REFERENCES categories(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    price REAL NOT NULL,
    hsn_code TEXT,
    tax_percent REAL DEFAULT 0,
    is_active INTEGER DEFAULT 1,
    enabled INTEGER DEFAULT 1,
    is_favourite INTEGER DEFAULT 0,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT
);

CREATE TABLE IF NOT EXISTS tables (
    id TEXT PRIMARY KEY,
    store_id TEXT REFERENCES stores(id) ON DELETE CASCADE,
    number TEXT NOT NULL,
    seats INTEGER NOT NULL,
    position_x INTEGER DEFAULT 0,
    position_y INTEGER DEFAULT 0,
    is_active INTEGER DEFAULT 1,
    section TEXT DEFAULT NULL,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT,
    UNIQUE(store_id, number)
);

CREATE TABLE IF NOT EXISTS orders (
    id TEXT PRIMARY KEY,
    store_id TEXT REFERENCES stores(id) ON DELETE CASCADE,
    table_id TEXT REFERENCES tables(id),
    table_number TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'completed', 'cancelled')),
    order_type TEXT DEFAULT 'dine_in',
    customer_name TEXT,
    customer_mobile TEXT,
    total_amount REAL NOT NULL DEFAULT 0,
    tax_amount REAL DEFAULT 0,
    discount_amount REAL DEFAULT 0,
    payment_method TEXT,
    payment_status TEXT DEFAULT 'pending',
    created_by TEXT REFERENCES users(id),
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    cancelled_at TEXT,
    modified_by TEXT
);

CREATE TABLE IF NOT EXISTS order_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    order_id TEXT REFERENCES orders(id) ON DELETE CASCADE,
    item_id TEXT REFERENCES items(id),
    quantity INTEGER NOT NULL,
    unit_price REAL NOT NULL,
    tax_percent REAL DEFAULT 0,
    notes TEXT,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT
);

CREATE TABLE IF NOT EXISTS bills (
    id TEXT PRIMARY KEY,
    store_id TEXT REFERENCES stores(id) ON DELETE CASCADE,
    order_id TEXT REFERENCES orders(id),
    table_number TEXT NOT NULL,
    invoice_no TEXT,
    subtotal REAL NOT NULL,
    tax_total REAL NOT NULL,
    discount REAL DEFAULT 0,
    total REAL NOT NULL,
    payment_method TEXT,
    customer_name TEXT,
    customer_mobile TEXT,
    is_printed INTEGER DEFAULT 0,
    status TEXT DEFAULT 'active' CHECK (status IN ('active', 'cancelled')),
    generated_at TEXT DEFAULT {ts},
    generated_by TEXT REFERENCES users(id),
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT
);

CREATE TABLE IF NOT EXISTS bill_queue (
    id TEXT PRIMARY KEY,
    store_id TEXT REFERENCES stores(id) ON DELETE CASCADE,
    order_id TEXT REFERENCES orders(id) ON DELETE CASCADE,
    bill_data TEXT NOT NULL,
    status TEXT DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    error_message TEXT,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT
);

CREATE TABLE IF NOT EXISTS settings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    store_id TEXT REFERENCES stores(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT,
    UNIQUE(store_id, key)
);

CREATE TABLE IF NOT EXISTS global_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT
);

CREATE TABLE IF NOT EXISTS app_updates (
    id TEXT PRIMARY KEY,
    platform TEXT NOT NULL CHECK (platform IN ('mobile', 'desktop')),
    enabled INTEGER DEFAULT 0,
    version TEXT NOT NULL,
    download_url TEXT NOT NULL,
    release_notes TEXT,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT,
    UNIQUE(platform)
);

CREATE TABLE IF NOT EXISTS expense_categories (
    id TEXT PRIMARY KEY,
    store_id TEXT REFERENCES stores(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    is_active INTEGER DEFAULT 1,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT
);

CREATE TABLE IF NOT EXISTS expenses (
    id TEXT PRIMARY KEY,
    store_id TEXT REFERENCES stores(id) ON DELETE CASCADE,
    category_id TEXT REFERENCES expense_categories(id) ON DELETE SET NULL,
    title TEXT NOT NULL,
    description TEXT,
    amount REAL NOT NULL,
    expense_date TEXT NOT NULL,
    payment_method TEXT,
    receipt_number TEXT,
    vendor TEXT,
    attachments TEXT DEFAULT '[]',
    is_active INTEGER DEFAULT 1,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    created_by TEXT REFERENCES users(id),
    modified_by TEXT
);

CREATE TABLE IF NOT EXISTS item_expenses (
    id TEXT PRIMARY KEY,
    store_id TEXT REFERENCES stores(id) ON DELETE CASCADE,
    item_id TEXT REFERENCES items(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    amount REAL NOT NULL,
    is_active INTEGER DEFAULT 1,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT
);

CREATE TABLE IF NOT EXISTS table_sections (
    id TEXT PRIMARY KEY,
    store_id TEXT REFERENCES stores(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT,
    UNIQUE(store_id, name)
);

-- Outbox for offline-first cloud sync. Every successful mutating API call is
-- recorded here and replayed to the cloud backend by the sync worker.
CREATE TABLE IF NOT EXISTS sync_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    body TEXT,
    store_id TEXT,
    attempts INTEGER DEFAULT 0,
    pushed INTEGER DEFAULT 0,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT
);
CREATE INDEX IF NOT EXISTS idx_sync_outbox_pending ON sync_outbox(pushed, id);

-- Last-write-wins ordering: per-entity timestamp of the most recent
-- mutation's ORIGIN time. Written on local mutations (enqueue) and on
-- replayed cloud events; stale incoming events are skipped.
CREATE TABLE IF NOT EXISTS sync_entity_ts (
    entity_key TEXT PRIMARY KEY,
    last_ts TEXT NOT NULL,
    created_at TEXT DEFAULT {ts},
    updated_at TEXT DEFAULT {ts},
    modified_by TEXT
);
"#,
        ts = ts
    );

    conn.execute_batch(&ddl)?;

    // Additive column migrations for databases created by older builds.
    let alters = [
        "ALTER TABLE stores ADD COLUMN printer_name TEXT",
        "ALTER TABLE stores ADD COLUMN kot_print_enabled INTEGER DEFAULT 1",
        "ALTER TABLE stores ADD COLUMN remote_billing_enabled INTEGER DEFAULT 0",
        "ALTER TABLE stores ADD COLUMN logo_url TEXT",
        "ALTER TABLE stores ADD COLUMN theme_color TEXT",
        "ALTER TABLE stores ADD COLUMN tax_enabled INTEGER DEFAULT 1",
        "ALTER TABLE stores ADD COLUMN default_tax_percent REAL DEFAULT 0",
        "ALTER TABLE orders ADD COLUMN order_type TEXT DEFAULT 'dine_in'",
        "ALTER TABLE orders ADD COLUMN customer_name TEXT",
        "ALTER TABLE orders ADD COLUMN customer_mobile TEXT",
        "ALTER TABLE orders ADD COLUMN cancelled_at TEXT",
        "ALTER TABLE bills ADD COLUMN customer_mobile TEXT",
        "ALTER TABLE bills ADD COLUMN status TEXT DEFAULT 'active'",
        "ALTER TABLE tables ADD COLUMN section TEXT DEFAULT NULL",
        "ALTER TABLE categories ADD COLUMN enabled INTEGER DEFAULT 1",
        "ALTER TABLE items ADD COLUMN enabled INTEGER DEFAULT 1",
        "ALTER TABLE categories ADD COLUMN is_favourite INTEGER DEFAULT 0",
        "ALTER TABLE items ADD COLUMN is_favourite INTEGER DEFAULT 0",
    ];
    for q in alters {
        let _ = conn.execute(q, []);
    }

    // Audit columns on every table. SQLite rejects non-constant defaults in
    // ADD COLUMN, so migrated DBs get NULLABLE columns and the audit triggers
    // below backfill timestamps on write. No FK on modified_by — audit trails
    // must not block user deletion.
    let audit_tables = [
        "stores", "users", "user_stores", "categories", "items", "tables",
        "orders", "order_items", "bills", "bill_queue", "settings",
        "global_settings", "app_updates", "expense_categories", "expenses",
        "item_expenses", "table_sections", "sync_outbox", "sync_entity_ts",
    ];
    for t in audit_tables {
        for col in ["created_at TEXT", "updated_at TEXT", "modified_by TEXT"] {
            let _ = conn.execute(&format!("ALTER TABLE {t} ADD COLUMN {col}"), []);
        }
    }

    // One-row scratch table holding the acting user — the API dispatcher sets
    // it to the authenticated user before running a request and clears it
    // after, so background writers (sync worker, cleanup) leave modified_by
    // NULL. It must live in the main schema: triggers can't see temp tables.
    // Deliberately excluded from audit_tables — it has no triggers of its own.
    conn.execute_batch("CREATE TABLE IF NOT EXISTS session_actor (id TEXT)")?;

    // Audit triggers stamp created_at/updated_at and copy session_actor into
    // modified_by. recursive_triggers stays OFF, so the UPDATE inside a
    // trigger body cannot fire further triggers (no loops, no double-stamp).
    for t in audit_tables {
        conn.execute_batch(&format!(
            "DROP TRIGGER IF EXISTS audit_ins_{t};
             CREATE TRIGGER audit_ins_{t} AFTER INSERT ON {t} BEGIN
                 UPDATE {t} SET
                     created_at = COALESCE(created_at, {ts}),
                     updated_at = COALESCE(updated_at, {ts}),
                     modified_by = COALESCE(modified_by, (SELECT id FROM session_actor LIMIT 1))
                 WHERE rowid = NEW.rowid;
             END;
             DROP TRIGGER IF EXISTS audit_upd_{t};
             CREATE TRIGGER audit_upd_{t} AFTER UPDATE ON {t} BEGIN
                 UPDATE {t} SET
                     updated_at = {ts},
                     modified_by = COALESCE((SELECT id FROM session_actor LIMIT 1), modified_by)
                 WHERE rowid = NEW.rowid;
             END;",
            t = t,
            ts = TS_DEFAULT
        ))?;
    }

    Ok(())
}

pub fn run_seeds(conn: &Connection) -> Result<(), rusqlite::Error> {
    // Default global settings
    conn.execute_batch(
        "INSERT OR IGNORE INTO global_settings (key, value) VALUES
            ('cleanup_enabled', 'false'),
            ('cleanup_interval_mins', '60'),
            ('update_github_repo', 'ntoric/mario_tauri');",
    )?;

    // Default store
    let store_count: i64 = conn.query_row("SELECT COUNT(*) FROM stores", [], |r| r.get(0))?;
    if store_count == 0 {
        conn.execute(
            "INSERT INTO stores (id, name, branch, location, phone, invoice_size)
             VALUES ('1', 'Main Cafe', 'Main Branch', 'City Center', '+1234567890', '3inch')",
            [],
        )?;
    }

    // Superadmin
    let superadmin_username =
        std::env::var("SUPERADMIN_USERNAME").unwrap_or_else(|_| "superadmin".to_string());
    let superadmin_password =
        std::env::var("SUPERADMIN_PASSWORD").unwrap_or_else(|_| "superadmin123".to_string());
    let superadmin_name =
        std::env::var("SUPERADMIN_NAME").unwrap_or_else(|_| "Super Administrator".to_string());

    let admin_exists: bool = conn.query_row(
        "SELECT EXISTS(SELECT 1 FROM users WHERE role = 'superadmin')",
        [],
        |r| r.get(0),
    )?;

    let password_hash = auth::hash_password(&superadmin_password);
    if !admin_exists {
        conn.execute(
            "INSERT INTO users (id, username, password, name, email, role, is_active)
             VALUES (?1, ?2, ?3, ?4, ?5, 'superadmin', 1)",
            rusqlite::params![
                "11111111-1111-1111-1111-111111111111",
                superadmin_username,
                password_hash,
                superadmin_name,
                "admin@cafe.com"
            ],
        )?;
    }

    // Sample business owner
    let owner_exists: bool = conn.query_row(
        "SELECT EXISTS(SELECT 1 FROM users WHERE username = 'owner')",
        [],
        |r| r.get(0),
    )?;
    if !owner_exists {
        let owner_id = "22222222-2222-2222-2222-222222222222";
        let owner_hash = auth::hash_password("password");
        conn.execute(
            "INSERT INTO users (id, username, password, name, email, role, is_active)
             VALUES (?1, 'owner', ?2, 'Business Owner', 'owner@cafe.com', 'business_owner', 1)",
            rusqlite::params![owner_id, owner_hash],
        )?;
        conn.execute(
            "INSERT OR IGNORE INTO user_stores (user_id, store_id) VALUES (?1, '1')",
            [owner_id],
        )?;
    }

    Ok(())
}
