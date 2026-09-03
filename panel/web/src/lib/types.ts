/**
 * The shapes the API returns.
 *
 * Only what this application reads. The panel passes ratline's own JSON straight
 * through for sites, tenants, certificates and the rest, and those are typed loosely
 * on purpose: pinning a full mirror of ratline's models here would mean a ratline
 * release that adds a field breaks the build of an interface that does not use it.
 */

export type Role = 'superadmin' | 'admin';

export interface Account {
  id: string;
  email: string;
  name?: string;
  role: Role;
  totp_enabled: boolean;
  disabled: boolean;
  created_at: string;
  created_by?: string;
  last_login_at?: string;
  last_login_ip?: string;
}

export interface Me {
  account: Account;
  capabilities: {
    manage_team: boolean;
    destructive: boolean;
    require_totp: boolean;
    needs_totp_now: boolean;
  };
  panel: { domain?: string; version?: string; ratline_version?: string };
  /** This session's CSRF token, re-issued on every load so a reload keeps working. */
  csrf: string;
}

export interface Bootstrap {
  needs_setup: boolean;
  require_totp: boolean;
  product: string;
}

export interface ActionFlag {
  name: string;
  type: string;
  usage: string;
  default?: string;
  required: boolean;
  repeatable?: boolean;
  runtime?: string[];
}

export interface ActionArg {
  name: string;
  required: boolean;
}

export interface StdinSpec {
  label: string;
  help: string;
  /** When set, the form also asks for a name and the server composes NAME=value. */
  key_label?: string;
}

export interface Action {
  id: string;
  verb: string;
  title: string;
  summary: string;
  description?: string;
  group: string;
  args?: ActionArg[];
  flags?: ActionFlag[];
  mutates: boolean;
  destructive: boolean;
  long: boolean;
  min_role: Role;
  /** The value this action reads from standard input, if any. */
  stdin?: StdinSpec;
  examples?: string[];
}

export interface RunResult {
  action: string;
  argv?: string[];
  dry_run?: boolean;
  ok: boolean;
  exit_code?: number;
  data?: unknown;
  logs?: string;
  error?: { code: number; name: string; message: string; hint?: string };
  job_id?: string;
}

export type JobState = 'queued' | 'running' | 'done' | 'failed';

export interface Job {
  id: string;
  action: string;
  target?: string;
  argv?: string;
  actor?: string;
  state: JobState;
  queued_at: string;
  started_at?: string;
  finished_at?: string;
  exit_code: number;
  error?: string;
  hint?: string;
  output?: string;
  dry_run: boolean;
}

export interface ActionRecord {
  id: number;
  at: string;
  actor?: string;
  action: string;
  argv?: string;
  target?: string;
  dry_run: boolean;
  ok: boolean;
  exit_code: number;
  error?: string;
  duration_ms: number;
  ip?: string;
}

export interface Overview {
  status?: RatlineStatus;
  jobs?: Job[];
  recent?: ActionRecord[];
  warning?: string;
}

/** A loose reading of `ratline status --json`: only the fields shown. */
export interface RatlineStatus {
  [key: string]: unknown;
}

export interface Site {
  domain: string;
  user: string;
  runtime: string;
  enabled: boolean;
  [key: string]: unknown;
}

export interface Tenant {
  name: string;
  home?: string;
  shell?: string;
  disabled?: boolean;
  [key: string]: unknown;
}

export interface TeamView {
  accounts: Account[];
  invites: Invite[];
}

export interface Invite {
  id: string;
  email: string;
  role: Role;
  status: 'pending' | 'accepted' | 'revoked' | 'expired';
  invited_by?: string;
  created_at: string;
  expires_at: string;
}

export interface Invited {
  invite: Invite;
  link: string;
  note: string;
}
