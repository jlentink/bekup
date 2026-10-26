-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: InsertUser :one
INSERT INTO users (email, display_name, password_hash, role)
VALUES ($1, $2, $3, $4)
RETURNING *;
