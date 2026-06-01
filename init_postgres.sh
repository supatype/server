#!/bin/bash
set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
	CREATE USER supatype_admin LOGIN CREATEROLE CREATEDB REPLICATION BYPASSRLS;

    -- Supatype super admin
    CREATE USER supatype_auth_admin NOINHERIT CREATEROLE LOGIN NOREPLICATION PASSWORD 'root';
    CREATE SCHEMA IF NOT EXISTS $DB_NAMESPACE AUTHORIZATION supatype_auth_admin;
    GRANT CREATE ON DATABASE postgres TO supatype_auth_admin;
    ALTER USER supatype_auth_admin SET search_path = '$DB_NAMESPACE';
EOSQL
