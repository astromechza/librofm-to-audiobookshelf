package librofm

import "time"

// Book is the canonical libro.fm audiobook shape. Combines fields seen across
// /api/v10/library (which includes user_metadata) and
// /api/v10/explore/audiobook_details/{isbn} (which omits it).
//
// Source: docs/01-libro-fm-protocol.md, cross-checked against
// burntcookie90/librofm-downloader and jedwards1230/libro-client.
type Book struct {
	ISBN            string         `json:"isbn"`
	Title           string         `json:"title"`
	Subtitle        string         `json:"subtitle,omitempty"`
	Authors         []string       `json:"authors,omitempty"`
	CoverURL        string         `json:"cover_url,omitempty"`
	Publisher       string         `json:"publisher,omitempty"`
	PublicationDate *time.Time     `json:"publication_date,omitempty"`
	Description     string         `json:"description,omitempty"`
	Genres          []Genre        `json:"genres,omitempty"`
	Series          string         `json:"series,omitempty"`
	SeriesNum       *float64       `json:"series_num,omitempty"`
	Abridged        bool           `json:"abridged,omitempty"`
	AudiobookInfo   *AudiobookInfo `json:"audiobook_info,omitempty"`
	// UserMetadata is populated by /library; absent in audiobook_details.
	UserMetadata *UserMetadata `json:"user_metadata,omitempty"`
}

// Genre is the libro.fm category tag. Only the name is interesting for sync.
type Genre struct {
	Name string `json:"name"`
}

// AudiobookInfo wraps the audio-specific details libro.fm attaches to a Book.
type AudiobookInfo struct {
	Narrators     []string   `json:"narrators,omitempty"`
	Duration      int        `json:"duration,omitempty"` // seconds
	SizeBytes     int64      `json:"size_bytes,omitempty"`
	TrackCount    int        `json:"track_count,omitempty"`
	PartsCount    int        `json:"parts_count,omitempty"`
	PdfExtras     []PdfExtra `json:"pdf_extras,omitempty"`
	AudioLanguage string     `json:"audio_language,omitempty"`
}

// PdfExtra describes a downloadable PDF supplement.
type PdfExtra struct {
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

// UserMetadata is the per-user info present in /api/v10/library entries.
// We don't act on most fields in v1 but keep them for future progress sync.
type UserMetadata struct {
	Finished     bool   `json:"finished,omitempty"`
	TrackIndex   int    `json:"track_index,omitempty"`
	TrackSeconds int    `json:"track_seconds,omitempty"`
	AddedAt      string `json:"added_at,omitempty"`
	Hidden       bool   `json:"hidden,omitempty"`
}

// LibraryPage is one page of /api/v10/library.
type LibraryPage struct {
	Page       int      `json:"page"`
	TotalPages int      `json:"total_pages"`
	Audiobooks []Book   `json:"audiobooks"`
	Tags       []string `json:"tags,omitempty"`
}

// DownloadManifest is the response from /api/v10/download-manifest?isbn=X.
// Each part.URL is a presigned S3 URL to a ZIP of MP3 chapter files; URLs
// expire quickly (see ExpiresAt).
type DownloadManifest struct {
	ISBN      string         `json:"isbn"`
	Parts     []DownloadPart `json:"parts"`
	Tracks    []Track        `json:"tracks,omitempty"`
	ExpiresAt string         `json:"expires_at,omitempty"`
	Version   string         `json:"version,omitempty"`
	SizeBytes int64          `json:"size_bytes,omitempty"`
}

// DownloadPart is one ZIP archive in a multi-part MP3 download.
type DownloadPart struct {
	URL       string `json:"url"`
	SizeBytes int64  `json:"size_bytes"`
}

// Track is one chapter inside a DownloadManifest. chapter_title may be null.
type Track struct {
	Number       int    `json:"number"`
	LengthSec    int    `json:"length_sec,omitempty"`
	ChapterTitle string `json:"chapter_title,omitempty"`
}

// M4BResponse is the body of /api/v10/audiobooks/{isbn}/packaged_m4b on 200.
// On 404 (no M4B for this ISBN), the client returns ErrNoM4B instead.
type M4BResponse struct {
	URL string `json:"m4b_url"`
}

// loginRequest mirrors the OAuth2 password grant libro.fm expects.
type loginRequest struct {
	GrantType string `json:"grant_type"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}

// loginResponse is the /oauth/token response. Token has no expiry in practice.
type loginResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type,omitempty"`
	CreatedAt   int64  `json:"created_at,omitempty"`
}

// audiobookDetailsResponse wraps the /audiobook_details/{isbn} envelope.
type audiobookDetailsResponse struct {
	Data struct {
		Audiobook Book `json:"audiobook"`
	} `json:"data"`
}
