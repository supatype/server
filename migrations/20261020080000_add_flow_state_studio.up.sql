-- Marks an OAuth flow that Studio started (`/authorize?studio=1`). The callback then sends the
-- browser to the server's own Studio address, the API's external URL plus /studio/, and never to
-- a redirect_to the request carried. Kept on the server's flow state, so the browser holds only
-- the state's id and cannot set or change the flag.

/* auth_migration: 20261020080000 */
alter table {{ index .Options "Namespace" }}.flow_state
    add column if not exists studio boolean not null default false;
