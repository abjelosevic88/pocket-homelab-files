// Mirrors docs/api.md.
export interface User { id: number; email: string; name: string; username: string; role: 'admin' | 'member'; created_at: string }
export interface Person { id: number; name: string; email: string }

export interface AuthStatus { needs_setup: boolean; auth_mode: string; authenticated: boolean; user: User | null; version: string; title: string; oidc?: boolean; oidc_name?: string; core_configured?: boolean }

export type Perm = 'view' | 'edit'
export interface Space { id: string; kind: 'home' | 'mount' | 'share'; name: string; perm: Perm; is_dir: boolean; owner?: string; color?: string; read_only?: boolean }

export interface Entry { name: string; path: string; is_dir: boolean; size: number; modified: string; mime: string; starred?: boolean; shared?: number }
export interface Listing { space: Space; path: string; entry: Entry; entries: Entry[]; writable: boolean }

export interface Access { user_id: number; name?: string; email?: string; perm: Perm }
export interface Share { id: number; owner_id: number; owner_name: string; owner_email: string; path: string; is_dir: boolean; everyone_perm: '' | Perm; note: string; created_at: string; members?: Access[]; perm?: Perm; space?: string; name?: string }
export interface Mount { id: number; name: string; path: string; read_only: boolean; everyone_perm: '' | Perm; color: string; created_by: number; created_at: string; access?: Access[]; missing?: boolean; allowed?: boolean; error?: string }

export interface SendFile { idx: number; size: number; expected: number; name?: string; type?: string }
export interface Send {
  id: string; owner_id: number; kind: 'files' | 'note' | 'live'; encrypted: boolean; label: string
  live_space?: string; live_paths?: string[]; file_count: number; total_size: number
  has_password: boolean; expires_at: string; max_downloads: number; downloads: number; notify: boolean
  status: 'uploading' | 'ready'; last_download_at: string; created_at: string
  files?: SendFile[]; log?: { at: string; ip: string; agent: string }[]
  url: string; available: boolean; reason?: string
}
export interface PublicSend {
  id: string; kind: 'files' | 'note' | 'live'; encrypted: boolean; has_password: boolean; password_salt?: string; meta?: string
  file_count: number; total_size: number; expires_at: string; downloads_left: number; available: boolean; reason?: string
  files?: SendFile[]; entries?: Entry[]; ticket?: string; sizes?: number[]
}

export interface Request {
  id: string; owner_id: number; title: string; message: string; space: string; path: string; expires_at: string
  max_files: number; max_file_mb: number; has_password: boolean; notify: boolean; closed: boolean
  files_received: number; bytes_received: number; last_upload_at: string; created_at: string
  url: string; open: boolean; reason?: string; space_name: string; missing?: boolean
}

export interface TrashItem { id: number; name: string; orig_path: string; is_dir: boolean; size: number; deleted_by: number; deleted_at: string }
export interface Star { space: string; path: string; is_dir: boolean; created_at: string; space_name: string; entry: Entry | null; missing: boolean }

export interface Settings {
  title: string; setup_complete: boolean
  show_hidden: boolean; view: 'list' | 'grid'; sort: 'name' | 'modified' | 'size' | 'type'; sort_desc: boolean; timezone: string
  notify_kind: 'apprise' | 'ntfy' | 'webhook'; notify_url: string; notify_token?: string; notify_sends: boolean; notify_uploads: boolean
  send_expiry: string; send_downloads: number; send_encrypted: boolean; trash_days: number
  smtp_host: string; smtp_port: number; smtp_user: string; smtp_password: string; smtp_password_set: boolean; smtp_from: string; smtp_from_name: string; smtp_tls: 'none' | 'starttls' | 'tls'
  mail_subject: string; mail_body: string; mail_request_subject: string; mail_request_body: string
}

export interface APIToken { id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export interface SystemInfo {
  version: string; go: string; db_size: number; base_url: string; public_url: string; public_listener: boolean
  core: { configured: boolean; url: string; registered: boolean; error: string }; timezone: string
  webdav: { url: string; nextcloud_url: string; username: string }
  home: { files: number; size: number }; disk: { free: number; total: number }
  max_send_mb: number; max_send_days: number; mounting: boolean
  box_counts?: Record<string, number>; data_dir?: string; mount_roots?: string[]
}
