CREATE USER supatype_admin LOGIN CREATEROLE CREATEDB REPLICATION BYPASSRLS;

-- Supatype super admin
CREATE USER supatype_auth_admin NOINHERIT CREATEROLE LOGIN NOREPLICATION PASSWORD 'root';
CREATE SCHEMA IF NOT EXISTS auth AUTHORIZATION supatype_auth_admin;
GRANT CREATE ON DATABASE postgres TO supatype_auth_admin;
ALTER USER supatype_auth_admin SET search_path = 'auth';
