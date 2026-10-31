// Package externaltest starts a plain PostgreSQL testcontainer that stands
// in for an operator's external database (D-207): the official image
// without bekup's init scripts (no roles, no bekup database), TLS on with a
// certificate from a throw-away test CA (ECDSA P-256 or RSA, DNS and IP
// SANs), SCRAM for TCP logins and the image's superuser "postgres". Tests
// create a non-superuser admin with CREATEROLE and CREATEDB and run
// `bekup db bootstrap` / roles-sql against it.
//
// Everything but this file is built only with the integration tag
// (testcontainers stays out of the regular build).
package externaltest
