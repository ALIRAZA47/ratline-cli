import type { CommandGroup } from '../types';

export const databases: CommandGroup = {
  id: 'db',
  title: 'Databases',
  path: '/reference/db',
  blurb: 'MongoDB databases and least-privilege users, one per tenant.',
  intro: [
    'ratline never installs MongoDB as a side effect. It manages what lives inside a server you point it at — a local mongod or a managed cluster, the only difference being the connection string. The one explicit exception is `db install`, whose entire job is to put MongoDB on this host, secured, and it refuses a server somebody else set up. Nothing else will ever apt-get a database server because you asked for something adjacent.',
    'The admin connection string lives in a file at paths.mongo_uri_file, mode 0600, not in config.yaml: it is the root password for every database on the server, and config.yaml is a file operators paste into support tickets. ratline refuses to read it at any mode another account could see.',
    'Every role it grants is scoped to a single database. The cluster-wide ones — root, readWriteAnyDatabase, userAdminAnyDatabase — are deliberately absent, because granting one to a tenant’s application hands it every other tenant’s data, and it would be one flag away if the list were open.',
    'A password is generated, shown once, and never stored. MongoDB keeps a hash and will not return it, so ratline could not display it later even if it wanted to — which is the right shape rather than a limitation: there is no credential store here to be stolen, and a lost password is rotated rather than recovered.',
  ],
  commands: [
    {
      id: 'db-install',
      name: 'ratline db install',
      status: 'built',
      summary: 'Install MongoDB on this host, secure it, and attach it.',
      description: [
        'Adds MongoDB’s official apt repository, installs mongodb-org, creates a root-role admin user with the password you choose, replaces /etc/mongod.conf with a managed configuration that enables authorization and binds localhost only, restarts the server, and proves the outcome — the running server must enforce authorization and accept those credentials — before storing the connection string and turning provisioning on.',
        'This is the one ratline command that adds a package repository and installs software. The repository’s signing key ships inside the ratline binary and is pinned into the apt source with signed-by; nothing about the root of trust is downloaded at install time.',
        'A failure at any step is unwound — config restored, user removed, service stopped and disabled. Only the downloaded packages stay, inert, so a re-run continues where it left off. Re-running after success verifies and reports; it does not bounce a serving database.',
        'The server listens only on localhost until `db access allow` opens it, firewall first.',
      ],
      flags: [
        {
          name: '--stdin',
          type: 'bool',
          description: 'Read the admin password from stdin, for automation.',
          note: 'On a terminal the command prompts twice instead, and what you type is not echoed. The password is never a flag value: anything in argv is world-readable through /proc for as long as the command runs, and it would land in your shell history.',
        },
        {
          name: '--admin-user',
          arg: '<name>',
          type: 'string',
          default: 'admin',
          description: 'Name for the root-role admin user.',
        },
        {
          name: '--mongodb-version',
          arg: '<series>',
          type: 'string',
          description: 'Release series to install, such as 8.0. Default: the newest MongoDB publishes for this distribution release.',
        },
      ],
      refuses: [
        'A MongoDB server already installed on this host that ratline did not set up — enable authorization yourself and attach it with db connect.',
        'A host whose distribution release MongoDB’s repository does not publish packages for.',
        'A password shorter than 8 characters, or one containing control characters.',
      ],
      examples: [
        {
          title: 'Choose the password at the prompt: not echoed, not in argv',
          lang: 'shell',
          code: `ratline db install
ratline db create shop --owner acme`,
        },
        {
          title: 'For automation, where there is no terminal',
          lang: 'shell',
          code: 'ratline db install --stdin < /root/mongo-admin-password',
        },
      ],
      seeAlso: [
        { label: 'db connect', to: '/reference/db/connect' },
        { label: 'db access allow', to: '/reference/db/access-allow' },
      ],
      keywords: ['install mongodb', 'apt repository', 'mongod', 'authorization', 'root user'],
    },
    {
      id: 'db-connect',
      name: 'ratline db connect',
      status: 'built',
      summary: 'Point ratline at a MongoDB server and turn provisioning on.',
      description: [
        'Creates the directory at 0700, writes the connection string at 0600 root-owned, turns on features.db_provisioning, and proves the credentials work before committing any of it. If the server cannot be reached, or rejects them, nothing is left behind — a stored string that does not work is indistinguishable from a server that is down.',
        'This replaces four manual steps, two of which were about the mode of a file holding the root password for every database on the server. Getting that wrong is not a typo, it is every tenant able to read every other tenant’s data.',
      ],
      flags: [
        {
          name: '--stdin',
          type: 'bool',
          description: 'Read the connection string from stdin, for automation.',
          note: 'With no flags on a terminal the command prompts instead, and what you paste is not echoed. It is never a flag value: anything in argv is world-readable through /proc, so a password passed as an argument is visible to every account on the box for as long as the command runs — and it lands in your shell history, which outlives the password.',
        },
        {
          name: '--from-file',
          arg: '<path>',
          type: 'path',
          description: 'Read the connection string from a file instead.',
        },
        {
          name: '--force',
          type: 'bool',
          default: 'false',
          description: 'Replace a connection string that is already stored.',
        },
      ],
      examples: [
        {
          title: 'Paste it at the prompt: not echoed, not in argv, not in your history',
          lang: 'shell',
          code: `ratline db connect
ratline db ping`,
        },
        {
          title: 'For automation, where there is no terminal to prompt on',
          lang: 'shell',
          code: `ratline db connect --stdin < /root/mongodb.uri
ratline db connect --from-file /root/atlas.uri`,
        },
      ],
    },
    {
      id: 'db-enable',
      name: 'ratline db enable',
      status: 'built',
      summary: 'Turn database provisioning on.',
      description: [
        'Sets features.db_provisioning. Prefer `db connect` if no connection string is stored yet — that does both and proves the credentials first.',
        'It checks a usable connection string exists before turning the feature on, because a command group that only ever refuses is worse than one that is plainly off.',
      ],
      examples: [{ lang: 'shell', code: 'ratline db enable' }],
    },
    {
      id: 'db-disable',
      name: 'ratline db disable',
      status: 'built',
      summary: 'Turn database provisioning off.',
      description: [
        'Nothing on the MongoDB server is touched: the databases, their users and every site holding a credential keep working. This only stops ratline managing them.',
        '--forget also removes the stored admin connection string, which is what handing a server over looks like — and worth not doing otherwise, because it is the one copy ratline has.',
      ],
      flags: [
        { name: '--forget', type: 'bool', default: 'false', description: 'Also remove the stored admin connection string.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline db disable' }],
    },
    {
      id: 'db-ping',
      name: 'ratline db ping',
      status: 'built',
      summary: 'Check the server is reachable and enforcing authentication.',
      description: [
        'Reports the version and topology, and whether authentication is actually enforced. That last one is worth reading: a mongod started without --auth answers every command from anyone who can reach the port, so the users ratline creates would be decoration.',
        'The admin password is redacted from the output, and from everything ratline logs or records.',
      ],
      examples: [{ lang: 'shell', code: 'ratline db ping' }],
    },
    {
      id: 'db-create',
      name: 'ratline db create',
      args: '<name>',
      status: 'built',
      summary: 'Create a database, a user scoped to it, and optionally attach it to a site.',
      description: [
        'Creates the database, creates a user whose only role is on that database, and prints the connection string once.',
        'MongoDB has no createDatabase — a database exists once something is written into it — so an initial collection is created too. Without it a new database is invisible to `db list` until the application writes, which reads as the create having silently failed.',
      ],
      flags: [
        {
          name: '--owner',
          arg: '<tenant>',
          type: 'string',
          description: 'Tenant that owns this database. Required.',
          note: 'This is how a later `user delete --purge` knows what to revoke. Without an owner a database outlives the account it was created for, and nothing cleans it up.',
        },
        {
          name: '--user',
          arg: '<name>',
          type: 'string',
          default: '<database>_app',
          description: 'Username to create alongside the database.',
        },
        {
          name: '--role',
          arg: '<role>',
          type: 'enum',
          default: 'databases.mongodb.default_role (readWrite)',
          description: 'Role on this database: read, readWrite, dbAdmin or dbOwner.',
        },
        {
          name: '--attach',
          arg: '<domain>',
          type: 'string',
          description: 'Write the connection string into that site’s .env instead of printing it.',
          note: 'This is the flag to prefer. The URI goes into a 0600 file owned by the tenant rather than onto your terminal, which keeps the password out of shell history and scrollback — both of which outlive every rotation. The application picks it up on its next restart, because an environment variable is read at startup.',
        },
        {
          name: '--env-key',
          arg: '<NAME>',
          type: 'string',
          default: 'databases.mongodb.env_key (MONGODB_URI)',
          description: 'Variable name to write for --attach.',
        },
        {
          name: '--no-user',
          type: 'bool',
          default: 'false',
          description: 'Create the database only, with no user — for adopting an existing schema.',
        },
      ],
      refuses: [
        'A name MongoDB cannot address unambiguously: anything containing a dot, a slash, a space, or one of $*<>:|?" — a dot is a namespace separator and would make the database unnameable in a role document.',
        'admin, local and config. Provisioning inside the server’s own databases can destroy its credentials or its replication log.',
        'An owner that is not a tenant on this server.',
        'A name over 38 characters, which leaves no room for a collection inside MongoDB’s 64-byte namespace limit.',
      ],
      examples: [
        { lang: 'shell', code: 'ratline db create shop --owner acme' },
        {
          title: 'The usual case: hand it straight to the site',
          lang: 'shell',
          code: `ratline db create shop --owner acme --attach shop.example.com
ratline site restart shop.example.com`,
        },
      ],
    },
    {
      id: 'db-list',
      name: 'ratline db list',
      status: 'built',
      summary: 'Databases ratline provisioned, or everything on the server.',
      description: [
        'By default this reads ratline’s index. --live asks the server and marks anything it does not recognise.',
        'The difference is the useful part: a database on the server with no row was created outside ratline, and nothing will revoke its users when the tenant is deleted.',
      ],
      flags: [
        { name: '--live', type: 'bool', default: 'false', description: 'Ask the server rather than reading the index.' },
        { name: '--owner', arg: '<tenant>', type: 'string', description: 'Only this tenant’s databases.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline db list --live' }],
    },
    {
      id: 'db-show',
      name: 'ratline db show',
      args: '<name>',
      status: 'built',
      summary: 'One database, its users, and what it actually holds.',
      description: [
        'Collections, document count and index size come from the server, which is the difference between “provisioned” and “in use”.',
        'Users the server has but ratline does not are named: they will survive a tenant deletion.',
      ],
      examples: [{ lang: 'shell', code: 'ratline db show shop' }],
    },
    {
      id: 'db-dump',
      name: 'ratline db dump',
      args: '<database>',
      status: 'built',
      summary: 'Write a compressed archive of one database.',
      description: [
        'One gzipped archive file, scoped to the named database, written 0600. It holds every document in the database, so where it goes afterwards is your responsibility — the same warning `ratline backup` carries about the .env.',
        '`ratline backup` archives a site’s files and nothing else, so without this a site with a database was backed up by two mechanisms, one of which did not exist.',
        'The connection string never appears in the argument list. /proc is world-readable, and an admin URI on a command line is the password for every database on the server, visible to every account on it for as long as the dump runs.',
      ],
      flags: [
        { name: '--out', arg: '<dir>', type: 'string',
          description: 'Directory to write into (default <backup_dir>/databases).' },
      ],
      examples: [{ lang: 'shell', code: 'ratline db dump app_example_com' }],
      seeAlso: [{ label: 'ratline db restore', to: '/reference/db/restore' }],
    },
    {
      id: 'db-restore',
      name: 'ratline db restore',
      args: '<archive>',
      status: 'built',
      summary: 'Load an archive back into a database.',
      description: [
        'Restores what `ratline db dump` wrote. By default it goes back into the database it came from, which the filename records.',
        'Documents already there are left alone unless --drop says otherwise, so a restore over a live database merges rather than replaces. That is the safer default and rarely the one you want: --drop is what makes it a restore, and it is confirmed by typing the database name.',
      ],
      flags: [
        { name: '--into', arg: '<database>', type: 'string',
          description: 'Restore into this database instead of the one it came from.',
          note: 'How a production dump reaches staging without editing the archive. An archive ratline did not write has no database in its name, so it asks for this rather than guessing which database to overwrite.' },
        { name: '--drop', type: 'bool', default: 'false',
          description: 'Replace what is there rather than merging into it.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline db restore app_example_com-20260807T120000Z.archive.gz --drop' },
        { title: 'Into staging instead', lang: 'shell',
          code: 'ratline db restore app.archive.gz --into app_staging' },
      ],
      seeAlso: [{ label: 'ratline db dump', to: '/reference/db/dump' }],
    },
    {
      id: 'db-drop',
      name: 'ratline db drop',
      args: '<name>',
      status: 'built',
      summary: 'Drop a database and its users.',
      description: [
        'Destroys data and cannot be undone, so it names the document and collection count before asking, and needs the database’s name typed back. A count is what makes somebody stop; “are you sure?” is not.',
        'Users are removed before the database, deliberately: dropping a database does not remove its users, and a user left behind still authenticates and still holds a role on a database that springs back into existence the moment anything writes through it.',
      ],
      flags: [
        {
          name: '--keep-database',
          type: 'bool',
          default: 'false',
          description: 'Remove the users and ratline’s record, but leave the data.',
          note: 'What handing a database over to someone else’s tooling looks like.',
        },
        { name: '--force', type: 'bool', default: 'false', description: 'Skip the confirmation.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline db drop shop' }],
    },
    {
      id: 'db-user-add',
      name: 'ratline db user add',
      args: '<username>',
      status: 'built',
      summary: 'Create a user scoped to one database.',
      description: [
        'A second, narrower credential on the same database is how you give something less access than the application has: a reporting job with read, a migration tool with dbAdmin, each revocable on its own without touching the others.',
      ],
      flags: [
        { name: '--database', arg: '<name>', type: 'string', description: 'Database this user has a role on. Required.' },
        { name: '--role', arg: '<role>', type: 'enum', default: 'readWrite', description: 'Role to grant.' },
        { name: '--attach', arg: '<domain>', type: 'string', description: 'Write the connection string into that site’s .env.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline db user add reports --database shop --role read' }],
    },
    {
      id: 'db-user-password',
      name: 'ratline db user password',
      args: '<username>',
      status: 'built',
      summary: 'Rotate a user’s password.',
      description: [
        'The old password stops working immediately, so anything still using it fails until it gets the new one.',
        '--all-sites updates every site recorded as holding this credential, which is the difference between a rotation and an outage. If a site cannot be updated it is named loudly, because by then the password has already changed on the server and that site is down.',
        'The applications still need restarting: an environment variable is read at startup.',
      ],
      flags: [
        {
          name: '--all-sites',
          type: 'bool',
          default: 'false',
          description: 'Update every site already holding this credential.',
        },
        { name: '--attach', arg: '<domain>', type: 'string', description: 'Also write the new string into this site’s .env.' },
        { name: '--auth-db', arg: '<name>', type: 'string', description: 'Authentication database, when the username is ambiguous.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline db user password shop_app --all-sites' }],
    },
    {
      id: 'db-user-grant',
      name: 'ratline db user grant',
      args: '<username>',
      status: 'built',
      summary: 'Change a user’s role on its database.',
      description: [
        'Replaces the user’s roles with exactly the one named, rather than adding to them. That is deliberate: grant means “this user should have exactly this access”, and accumulating roles quietly is how a read-only user ends up able to write.',
        'The password is unchanged, so nothing needs restarting.',
      ],
      flags: [{ name: '--role', arg: '<role>', type: 'enum', description: 'Role to grant, replacing any existing. Required.' }],
      examples: [{ lang: 'shell', code: 'ratline db user grant reports --role read' }],
    },
    {
      id: 'db-user-list',
      name: 'ratline db user list',
      status: 'built',
      summary: 'Users, their roles, and the sites holding their credentials.',
      flags: [
        { name: '--database', arg: '<name>', type: 'string', description: 'Only users of this database.' },
        {
          name: '--live',
          type: 'bool',
          default: 'false',
          description: 'Ask the server. Needs --database.',
          note: 'MongoDB keeps users per database, so there is no server-wide listing that does not read the admin database directly.',
        },
      ],
      examples: [{ lang: 'shell', code: 'ratline db user list --database shop --live' }],
    },
    {
      id: 'db-user-delete',
      name: 'ratline db user delete',
      args: '<username>',
      status: 'built',
      summary: 'Remove a user. Its data is untouched.',
      description: [
        'A user is a credential, not a container, so the data stays.',
        'Any site holding this user’s connection string is named before anything happens: removing the user takes that site’s database access with it.',
      ],
      flags: [{ name: '--force', type: 'bool', default: 'false', description: 'Do not ask, even when a site depends on it.' }],
      examples: [{ lang: 'shell', code: 'ratline db user delete reports' }],
    },
    {
      id: 'db-roles',
      name: 'ratline db roles',
      status: 'built',
      summary: 'The roles ratline will grant, and what each allows.',
      description: [
        'read, readWrite, dbAdmin and dbOwner — every one scoped to a single database.',
        'If you genuinely need a cluster-wide role, use mongosh directly. ratline will not be the thing that made it easy.',
      ],
      examples: [{ lang: 'shell', code: 'ratline db roles' }],
    },
    {
      id: 'db-access-allow',
      name: 'ratline db access allow',
      args: '<address>',
      status: 'built',
      summary: 'Let an address or network reach MongoDB.',
      description: [
        'Adds a ufw rule admitting the address to port 27017. The first allowed address also reconfigures mongod to listen beyond localhost and restarts it — firewall rule first, wider bind second, so the guard is standing before the door opens. The outcome is verified against the running server: still enforcing authorization, and actually listening where the config says.',
        'The address can be one machine (203.0.113.19) or a network in CIDR notation (10.8.0.0/24). Prefer the narrowest thing that works: everyone the rule admits faces only the password from then on.',
        'This manages the mongod that db install set up. For a server elsewhere — Atlas, another host — the access list lives with that server, and this refuses.',
      ],
      flags: [
        { name: '--note', arg: '<text>', type: 'string', description: 'A word on whose address this is, shown in the list.' },
      ],
      refuses: [
        'ufw not installed — without a firewall, an allowed-addresses list is fiction.',
        'ufw inactive — ratline never runs ufw enable for you: done in the wrong order it locks you out of SSH. Allow SSH first, then enable it yourself.',
        'A default incoming policy of allow — an allow-list on an allow-by-default firewall restricts nobody.',
        'A mongod whose configuration ratline does not manage.',
      ],
      examples: [
        {
          lang: 'shell',
          code: `ratline db access allow 203.0.113.19
ratline db access allow 10.8.0.0/24 --note "office vpn"`,
        },
      ],
      seeAlso: [
        { label: 'db access list', to: '/reference/db/access-list' },
        { label: 'db access revoke', to: '/reference/db/access-revoke' },
      ],
      keywords: ['whitelist', 'allowlist', 'firewall', 'ufw', 'bindIp', 'remote access', 'expose'],
    },
    {
      id: 'db-access-revoke',
      name: 'ratline db access revoke',
      args: '<address>',
      status: 'built',
      summary: 'Stop an address reaching MongoDB.',
      description: [
        'Deletes the ufw rule an allow created. Revoking the last allowed address also puts mongod back to listening on localhost only, restarts it, and verifies both facts against the running server.',
        'Connections the address already holds open are not cut — the firewall stops new ones. Revoking the last address cuts everything anyway, as a side effect of the rebind.',
        'Revoking an address that was never allowed reports as much and changes nothing: it is already the state you asked for.',
      ],
      examples: [{ lang: 'shell', code: 'ratline db access revoke 203.0.113.19' }],
      keywords: ['whitelist', 'firewall', 'ufw', 'close port'],
    },
    {
      id: 'db-access-list',
      name: 'ratline db access list',
      status: 'built',
      summary: 'Show who can reach this host’s MongoDB.',
      description: [
        'The allowed addresses, what mongod is bound to, and whether the firewall is still standing guard — together, because each is meaningless without the others.',
        'If mongod listens beyond localhost while ufw is inactive or defaulting to allow, this says so in as many words. doctor reports the same condition.',
      ],
      examples: [{ lang: 'shell', code: 'ratline db access list' }],
      keywords: ['whitelist', 'allowed addresses', 'firewall status'],
    },
  ],
};
