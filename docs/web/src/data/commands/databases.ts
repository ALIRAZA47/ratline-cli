import type { CommandGroup } from '../types';

export const databases: CommandGroup = {
  id: 'db',
  title: 'Databases',
  path: '/reference/db',
  blurb: 'MongoDB databases and least-privilege users, one per tenant.',
  intro: [
    'ratline does not install MongoDB. It manages what lives inside a server you point it at — a local mongod or a managed cluster, the only difference being the connection string. A database server is a stateful thing with backups and a replication topology, and a tool that silently apt-gets one has made a decision belonging to whoever owns the data. The same reasoning has ratline configure nginx and drive certbot without installing either.',
    'The admin connection string lives in a file at paths.mongo_uri_file, mode 0600, not in config.yaml: it is the root password for every database on the server, and config.yaml is a file operators paste into support tickets. ratline refuses to read it at any mode another account could see.',
    'Every role it grants is scoped to a single database. The cluster-wide ones — root, readWriteAnyDatabase, userAdminAnyDatabase — are deliberately absent, because granting one to a tenant’s application hands it every other tenant’s data, and it would be one flag away if the list were open.',
    'A password is generated, shown once, and never stored. MongoDB keeps a hash and will not return it, so ratline could not display it later even if it wanted to — which is the right shape rather than a limitation: there is no credential store here to be stolen, and a lost password is rotated rather than recovered.',
  ],
  commands: [
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
          description: 'Read the connection string from stdin. The usual way.',
          note: 'It is not a flag value on purpose. Anything in argv is world-readable through /proc, so a password passed as an argument is visible to every account on the box for as long as the command runs — and it lands in your shell history, which outlives the password.',
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
          title: 'From a password manager, or a file — never as an argument',
          lang: 'shell',
          code: `printf 'mongodb://admin:PASS@127.0.0.1:27017/?authSource=admin' \\
  | ratline db connect --stdin

ratline db ping`,
        },
        { lang: 'shell', code: 'ratline db connect --from-file /root/atlas.uri' },
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
  ],
};
