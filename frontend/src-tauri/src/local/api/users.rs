use rusqlite::OptionalExtension;
use serde_json::{json, Value};

use super::rows;
use super::{created, entity_id, err, json_to_sql, now_ts, ok, ApiResponse, Ctx};
use crate::local::auth;

const USER_COLS: &str =
    "u.id, u.username, u.password, u.name, u.email, u.role, u.store_id, u.is_active, u.created_at, COALESCE(s.name, '') as store_name";

/// Returns (user_json, password_hash) or None.
fn get_user(conn: &rusqlite::Connection, id: &str) -> Option<(Value, String)> {
    let sql = format!(
        "SELECT {} FROM users u LEFT JOIN stores s ON u.store_id = s.id WHERE u.id = ?1",
        USER_COLS
    );
    conn.query_row(&sql, [id], |r| {
        let pw: String = r.get(2)?;
        Ok((rows::user_json(conn, r), pw))
    })
    .optional()
    .ok()
    .flatten()
}

/// Whether an owner-scope caller may manage `target`: target's primary store
/// is one of the caller's stores, or they share a store via user_stores
/// (owner-role users keep their memberships there, not in users.store_id).
fn owner_can_manage(conn: &rusqlite::Connection, owner_id: &str, target: &Value) -> bool {
    let tid = target["id"].as_str().unwrap_or("");
    if tid == owner_id {
        return true;
    }
    let tsid = target["storeId"].as_str().unwrap_or("");
    if !tsid.is_empty() {
        let owned = conn
            .query_row(
                "SELECT EXISTS(SELECT 1 FROM user_stores WHERE user_id = ?1 AND store_id = ?2)",
                rusqlite::params![owner_id, tsid],
                |r| r.get(0),
            )
            .unwrap_or(false);
        if owned {
            return true;
        }
    }
    conn.query_row(
        "SELECT EXISTS(
             SELECT 1 FROM user_stores t
             JOIN user_stores o ON t.store_id = o.store_id
             WHERE t.user_id = ?1 AND o.user_id = ?2)",
        rusqlite::params![tid, owner_id],
        |r| r.get(0),
    )
    .unwrap_or(false)
}

/// Whether the caller may manage the given target user.
fn can_manage_user(ctx: &Ctx<'_>, claims: &crate::local::auth::Claims, target: &Value) -> bool {
    match claims.role.as_str() {
        "superadmin" => true,
        "business_owner" => owner_can_manage(&ctx.conn, &claims.id, target),
        "business_admin" => {
            let tsid = target["storeId"].as_str().unwrap_or("");
            !tsid.is_empty() && tsid == claims.store_id
        }
        _ => false,
    }
}

/// All store ids a caller may assign users to (empty vec = unrestricted for
/// superadmin semantics; callers gate on role before using it).
fn caller_store_ids(conn: &rusqlite::Connection, user_id: &str) -> Vec<String> {
    let mut stmt = match conn.prepare("SELECT store_id FROM user_stores WHERE user_id = ?1") {
        Ok(s) => s,
        Err(_) => return Vec::new(),
    };
    stmt.query_map([user_id], |r| r.get::<_, String>(0))
        .map(|rows| rows.filter_map(|r| r.ok()).collect())
        .unwrap_or_default()
}

/// GET /api/users
pub fn get_users(ctx: &mut Ctx) -> ApiResponse {
    let claims = match ctx.claims() {
        Ok(c) => c.clone(),
        Err(r) => return r,
    };

    let mut sql = format!(
        "SELECT {} FROM users u LEFT JOIN stores s ON u.store_id = s.id WHERE 1=1",
        USER_COLS
    );
    let mut args: Vec<String> = Vec::new();

    match claims.role.as_str() {
        "business_owner" => {
            sql += " AND (u.store_id IN (SELECT store_id FROM user_stores WHERE user_id = ?1) OR u.id = ?1)";
            args.push(claims.id.clone());
        }
        "business_admin" | "staff" => {
            sql += " AND u.store_id = ?1";
            args.push(claims.store_id.clone());
        }
        _ => {}
    }
    sql += " ORDER BY u.created_at DESC";

    let conn: &rusqlite::Connection = &ctx.conn;
    let mut stmt = match conn.prepare(&sql) {
        Ok(s) => s,
        Err(e) => return err(500, e.to_string()),
    };
    let users: Vec<Value> = match stmt.query_map(rusqlite::params_from_iter(args.iter()), |r| {
        Ok(rows::user_json(conn, r))
    }) {
        Ok(rows) => rows.filter_map(|r| r.ok()).collect(),
        Err(e) => return err(500, e.to_string()),
    };

    ok(json!(users))
}

/// POST /api/users
pub fn create_user(ctx: &mut Ctx, body: Value) -> ApiResponse {
    let claims = match ctx.claims() {
        Ok(c) => c.clone(),
        Err(r) => return r,
    };
    if !["superadmin", "business_owner", "business_admin"].contains(&claims.role.as_str()) {
        return err(403, "Not authorized");
    }

    let role = body["role"].as_str().unwrap_or("");
    if claims.role == "business_owner" && role == "superadmin" {
        return err(403, "Not authorized");
    }

    let mut final_store_id = body["storeId"].as_str().unwrap_or("").to_string();
    if claims.role == "business_admin" {
        if role != "business_admin" && role != "staff" {
            return err(
                403,
                "Business admin can only create Business Admin or Staff roles",
            );
        }
        let req_store = body["storeId"].as_str().unwrap_or("");
        if !req_store.is_empty() && req_store != claims.store_id {
            return err(403, "Can only assign users to your own store");
        }
        final_store_id = claims.store_id.clone();
    }

    // A business owner may only attach users to stores they own.
    if claims.role == "business_owner" {
        let owned = caller_store_ids(&ctx.conn, &claims.id);
        let in_scope = |sid: &str| owned.iter().any(|o| o == sid);
        if !final_store_id.is_empty() && !in_scope(&final_store_id) {
            return err(403, "Can only assign users to your own stores");
        }
        if let Some(ids) = body["storeIds"].as_array() {
            for sid in ids.iter().filter_map(|v| v.as_str()) {
                if !in_scope(sid) {
                    return err(403, "Can only assign users to your own stores");
                }
            }
        }
    }

    let id = entity_id(&body);
    let hashed = auth::hash_password(body["password"].as_str().unwrap_or(""));

    let tx = match ctx.conn.transaction() {
        Ok(t) => t,
        Err(e) => return err(500, e.to_string()),
    };

    let store_param: Option<String> = if final_store_id.is_empty() {
        None
    } else {
        Some(final_store_id.clone())
    };

    let res = tx.execute(
        "INSERT INTO users (id, username, password, name, email, role, store_id, is_active)
         VALUES (?1,?2,?3,?4,?5,?6,?7,1)",
        rusqlite::params![
            id,
            body["username"].as_str().unwrap_or(""),
            hashed,
            body["name"].as_str().unwrap_or(""),
            body["email"].as_str().unwrap_or(""),
            role,
            store_param,
        ],
    );
    if let Err(e) = res {
        return err(500, e.to_string());
    }

    if role == "business_owner" {
        if let Some(ids) = body["storeIds"].as_array() {
            for sid in ids {
                if let Some(sid) = sid.as_str() {
                    if let Err(e) = tx.execute(
                        "INSERT OR IGNORE INTO user_stores (user_id, store_id) VALUES (?1, ?2)",
                        rusqlite::params![id, sid],
                    ) {
                        return err(500, e.to_string());
                    }
                }
            }
        }
    }

    if let Err(e) = tx.commit() {
        return err(500, e.to_string());
    }

    created(json!({
        "id": id,
        "username": body["username"].as_str().unwrap_or(""),
        "name": body["name"].as_str().unwrap_or(""),
        "email": body["email"].as_str().unwrap_or(""),
        "role": role,
        "storeId": final_store_id,
        "isActive": true,
    }))
}

/// PUT /api/users/:id
pub fn update_user(ctx: &mut Ctx, id: &str, body: Value) -> ApiResponse {
    let claims = match ctx.claims() {
        Ok(c) => c.clone(),
        Err(r) => return r,
    };
    if !["superadmin", "business_owner", "business_admin"].contains(&claims.role.as_str()) {
        return err(403, "Not authorized");
    }

    let (target_json, _) = match get_user(&ctx.conn, id) {
        Some(t) => t,
        None => return err(404, "User not found"),
    };
    if target_json["role"].as_str() == Some("superadmin") && claims.role != "superadmin" {
        return err(403, "Not authorized");
    }
    if !can_manage_user(ctx, &claims, &target_json) {
        return err(403, "Not authorized");
    }

    // Store reassignment must stay inside the caller's own scope.
    let new_store_id = body["storeId"].as_str().unwrap_or("");
    if claims.role == "business_admin"
        && !new_store_id.is_empty()
        && new_store_id != claims.store_id
    {
        return err(403, "Can only assign users to your own store");
    }
    if claims.role == "business_owner" {
        let owned = caller_store_ids(&ctx.conn, &claims.id);
        let in_scope = |sid: &str| owned.iter().any(|o| o == sid);
        if !new_store_id.is_empty() && !in_scope(new_store_id) {
            return err(403, "Can only assign users to your own stores");
        }
        if let Some(ids) = body["storeIds"].as_array() {
            for sid in ids.iter().filter_map(|v| v.as_str()) {
                if !in_scope(sid) {
                    return err(403, "Can only assign users to your own stores");
                }
            }
        }
    }

    let mut fields: Vec<(&str, Value)> = Vec::new();
    for (json_key, col) in [
        ("name", "name"),
        ("email", "email"),
        ("isActive", "is_active"),
        ("storeId", "store_id"),
    ] {
        if let Some(v) = body.get(json_key) {
            if !v.is_null() {
                fields.push((col, v.clone()));
            }
        }
    }
    if claims.role == "superadmin" {
        if let Some(v) = body.get("role") {
            if !v.is_null() {
                fields.push(("role", v.clone()));
            }
        }
    }

    let has_store_ids = body.get("storeIds").is_some();
    let store_ids: Vec<String> = body["storeIds"]
        .as_array()
        .map(|a| {
            a.iter()
                .filter_map(|v| v.as_str().map(String::from))
                .collect()
        })
        .unwrap_or_default();

    let tx = match ctx.conn.transaction() {
        Ok(t) => t,
        Err(e) => return err(500, e.to_string()),
    };

    if !fields.is_empty() {
        let mut sets: Vec<String> = Vec::new();
        let mut args: Vec<rusqlite::types::Value> = Vec::new();
        for (i, (col, v)) in fields.iter().enumerate() {
            sets.push(format!("{} = ?{}", col, i + 1));
            args.push(json_to_sql(v));
        }
        let sql = format!(
            "UPDATE users SET {}, updated_at = ?{} WHERE id = ?{}",
            sets.join(", "),
            fields.len() + 1,
            fields.len() + 2
        );
        args.push(rusqlite::types::Value::Text(now_ts()));
        args.push(rusqlite::types::Value::Text(id.to_string()));
        if let Err(e) = tx.execute(&sql, rusqlite::params_from_iter(args.iter())) {
            return err(500, e.to_string());
        }
    }

    if has_store_ids {
        if let Err(e) = tx.execute("DELETE FROM user_stores WHERE user_id = ?1", [id]) {
            return err(500, e.to_string());
        }
        for sid in &store_ids {
            if let Err(e) = tx.execute(
                "INSERT OR IGNORE INTO user_stores (user_id, store_id) VALUES (?1, ?2)",
                rusqlite::params![id, sid],
            ) {
                return err(500, e.to_string());
            }
        }
    }

    if let Err(e) = tx.commit() {
        return err(500, e.to_string());
    }

    ok(json!({ "message": "User updated" }))
}

/// DELETE /api/users/:id
pub fn delete_user(ctx: &mut Ctx, id: &str) -> ApiResponse {
    let claims = match ctx.claims() {
        Ok(c) => c.clone(),
        Err(r) => return r,
    };
    if !["superadmin", "business_owner", "business_admin"].contains(&claims.role.as_str()) {
        return err(403, "Not authorized");
    }
    if id == claims.id {
        return err(400, "Cannot delete yourself");
    }

    let (target_json, _) = match get_user(&ctx.conn, id) {
        Some(t) => t,
        None => return err(404, "User not found"),
    };
    if target_json["role"].as_str() == Some("superadmin") {
        return err(403, "Cannot delete superadmin");
    }
    if !can_manage_user(ctx, &claims, &target_json) {
        return err(403, "Not authorized");
    }

    match ctx.conn.execute("DELETE FROM users WHERE id = ?1", [id]) {
        Ok(_) => ok(json!({ "message": "User deleted" })),
        Err(e) => err(500, e.to_string()),
    }
}

/// POST /api/users/change-password
pub fn change_password(ctx: &mut Ctx, body: Value) -> ApiResponse {
    let claims = match ctx.claims() {
        Ok(c) => c.clone(),
        Err(r) => return r,
    };

    let (_, pw_hash) = match get_user(&ctx.conn, &claims.id) {
        Some(t) => t,
        None => return err(404, "User not found"),
    };

    if !auth::verify_password(&pw_hash, body["currentPassword"].as_str().unwrap_or("")) {
        return err(401, "Current password is incorrect");
    }

    let hashed = auth::hash_password(body["newPassword"].as_str().unwrap_or(""));
    match ctx.conn.execute(
        "UPDATE users SET password = ?1 WHERE id = ?2",
        rusqlite::params![hashed, claims.id],
    ) {
        Ok(_) => ok(json!({ "message": "Password changed successfully" })),
        Err(e) => err(500, e.to_string()),
    }
}

/// POST /api/users/:id/reset-password
pub fn reset_password(ctx: &mut Ctx, id: &str, body: Value) -> ApiResponse {
    let claims = match ctx.claims() {
        Ok(c) => c.clone(),
        Err(r) => return r,
    };
    if claims.role != "superadmin" && claims.role != "business_owner" {
        return err(403, "Not authorized");
    }

    let (target_json, _) = match get_user(&ctx.conn, id) {
        Some(t) => t,
        None => return err(404, "User not found"),
    };

    if claims.role == "business_owner" {
        let stores = rows::user_stores(&ctx.conn, &claims.id).unwrap_or_default();
        let has_access = stores
            .iter()
            .any(|s| s["id"].as_str() == target_json["storeId"].as_str());
        if !has_access && id != claims.id {
            return err(403, "Not authorized to reset this user's password");
        }
    }

    if target_json["role"].as_str() == Some("superadmin") && claims.role != "superadmin" {
        return err(403, "Not authorized to reset superadmin password");
    }

    let hashed = auth::hash_password(body["password"].as_str().unwrap_or(""));
    match ctx.conn.execute(
        "UPDATE users SET password = ?1 WHERE id = ?2",
        rusqlite::params![hashed, id],
    ) {
        Ok(_) => ok(json!({ "message": "Password reset successfully" })),
        Err(e) => err(500, e.to_string()),
    }
}
