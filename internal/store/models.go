package store

// User is a person with their own files.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	Username     string `json:"username"` // WebDAV user name
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	FeedToken    string `json:"-"`
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
// It doubles as the app password WebDAV clients sign in with.
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Access is one person's permission on a mount or share.
type Access struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Perm   string `json:"perm"` // view | edit
}

// Mount is a server folder made visible in the app.
type Mount struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	Path         string   `json:"path,omitempty"` // only admins see it
	ReadOnly     bool     `json:"read_only"`
	EveryonePerm string   `json:"everyone_perm"`
	Color        string   `json:"color"`
	CreatedBy    int64    `json:"created_by"`
	CreatedAt    string   `json:"created_at"`
	Access       []Access `json:"access,omitempty"`
	// Perm is what the asking person may do here (view | edit).
	Perm string `json:"perm,omitempty"`
}

// Share hands one folder or file of the owner's home to other people.
type Share struct {
	ID           int64    `json:"id"`
	OwnerID      int64    `json:"owner_id"`
	OwnerName    string   `json:"owner_name"`
	OwnerEmail   string   `json:"owner_email"`
	Path         string   `json:"path"`
	IsDir        bool     `json:"is_dir"`
	EveryonePerm string   `json:"everyone_perm"`
	Note         string   `json:"note"`
	CreatedAt    string   `json:"created_at"`
	Members      []Access `json:"members,omitempty"`
	Perm         string   `json:"perm,omitempty"`
}

// Send is a link for anyone.
type Send struct {
	ID             string      `json:"id"`
	OwnerID        int64       `json:"owner_id"`
	Kind           string      `json:"kind"` // files | note | live
	Encrypted      bool        `json:"encrypted"`
	Label          string      `json:"label"`
	Meta           string      `json:"meta,omitempty"`
	LiveSpace      string      `json:"live_space,omitempty"`
	LivePaths      []string    `json:"live_paths,omitempty"`
	FileCount      int         `json:"file_count"`
	TotalSize      int64       `json:"total_size"`
	HasPassword    bool        `json:"has_password"`
	PasswordHash   string      `json:"-"`
	PasswordSalt   string      `json:"password_salt,omitempty"`
	AuthHash       string      `json:"-"`
	ExpiresAt      string      `json:"expires_at"`
	MaxDownloads   int         `json:"max_downloads"`
	Downloads      int         `json:"downloads"`
	Notify         bool        `json:"notify"`
	Status         string      `json:"status"`
	LastDownloadAt string      `json:"last_download_at"`
	CreatedAt      string      `json:"created_at"`
	Files          []SendFile  `json:"files,omitempty"`
	Log            []SendVisit `json:"log,omitempty"`
}

// SendFile is one stored blob of a send.
type SendFile struct {
	Idx      int    `json:"idx"`
	Size     int64  `json:"size"`
	Expected int64  `json:"expected"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"type,omitempty"`
}

// SendVisit is one counted download.
type SendVisit struct {
	At    string `json:"at"`
	IP    string `json:"ip"`
	Agent string `json:"agent"`
}

// Request is an upload link into one of the owner's folders.
type Request struct {
	ID            string `json:"id"`
	OwnerID       int64  `json:"owner_id"`
	Title         string `json:"title"`
	Message       string `json:"message"`
	Space         string `json:"space"`
	Path          string `json:"path"`
	ExpiresAt     string `json:"expires_at"`
	MaxFiles      int    `json:"max_files"`
	MaxFileMB     int64  `json:"max_file_mb"`
	PasswordHash  string `json:"-"`
	HasPassword   bool   `json:"has_password"`
	Notify        bool   `json:"notify"`
	Closed        bool   `json:"closed"`
	FilesReceived int    `json:"files_received"`
	BytesReceived int64  `json:"bytes_received"`
	LastUploadAt  string `json:"last_upload_at"`
	CreatedAt     string `json:"created_at"`
}

// Upload is a file arriving in pieces.
type Upload struct {
	ID       string `json:"id"`
	Owner    string `json:"owner"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Received int64  `json:"received"`
	Temp     string `json:"-"`
	Extra    string `json:"-"`
}

// TrashItem is something deleted from a home folder.
type TrashItem struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"-"`
	Name      string `json:"name"`
	OrigPath  string `json:"orig_path"`
	Stored    string `json:"-"`
	IsDir     bool   `json:"is_dir"`
	Size      int64  `json:"size"`
	DeletedBy int64  `json:"deleted_by"`
	DeletedAt string `json:"deleted_at"`
}

// Star is a pinned file or folder.
type Star struct {
	Space     string `json:"space"`
	Path      string `json:"path"`
	IsDir     bool   `json:"is_dir"`
	CreatedAt string `json:"created_at"`
}

// Settings are the box title plus one person's preferences.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`

	ShowHidden    bool   `json:"show_hidden"`
	View          string `json:"view"` // list | grid
	Sort          string `json:"sort"` // name | modified | size | type
	SortDesc      bool   `json:"sort_desc"`
	Timezone      string `json:"timezone"`
	NotifyKind    string `json:"notify_kind"`
	NotifyURL     string `json:"notify_url"`
	NotifyToken   string `json:"notify_token,omitempty"`
	NotifySends   bool   `json:"notify_sends"`   // someone downloaded a send
	NotifyUploads bool   `json:"notify_uploads"` // someone uploaded through a request
	SendExpiry    string `json:"send_expiry"`    // default expiry: 1h | 1d | 7d | 30d | never
	SendDownloads int    `json:"send_downloads"` // default download limit (0 = unlimited)
	SendEncrypted bool   `json:"send_encrypted"` // default for new sends
	TrashDays     int    `json:"trash_days"`

	// Email for sending links (the person's own SMTP account).
	SMTPHost        string `json:"smtp_host"`
	SMTPPort        int    `json:"smtp_port"`
	SMTPUser        string `json:"smtp_user"`
	SMTPPassword    string `json:"smtp_password"`
	SMTPPasswordSet bool   `json:"smtp_password_set"`
	SMTPFrom        string `json:"smtp_from"`
	SMTPFromName    string `json:"smtp_from_name"`
	SMTPTLS         string `json:"smtp_tls"` // none | starttls | tls
}
