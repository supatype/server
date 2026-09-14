package studioauth

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

// errNoPublishing is returned when this project has no versioned model.
//
// Not an error condition so much as an answer: the preview endpoints exist for
// every deployment, and on a project that never asked for drafts they say so
// rather than 404ing, which would read as a broken route.
var errNoPublishing = errors.New("no publishing configuration")

// errNoDatabase is returned when a deployment has no database configured.
var errNoDatabase = errors.New("no database")

// PublishingSettings is the draft and preview configuration, as the project
// stated it in `supatype.config.ts`.
//
// It reaches this process through `admin-config.json`, which the engine writes on
// every push and this package already reads for `adminRoles`. Settings that
// arrive the same way travel the same way: a second delivery path is a second
// thing to forget to wire, and this repo has been bitten by exactly that before.
type PublishingSettings struct {
	// DraftVisibility is the Studio roles that see every draft, beyond each
	// record's own creator.
	DraftVisibility []string
	// DefaultTTL is the lifetime of a link whose request named none, in seconds.
	DefaultTTL int
	// MaxRecordTTL bounds a link covering one record, in seconds.
	MaxRecordTTL int
	// MaxProjectTTL bounds a link covering every draft, in seconds. Shorter than
	// a record link by default: the blast radius is every unpublished draft
	// there is, so the window in which a leaked link is worth anything should be
	// smaller.
	MaxProjectTTL int
	// AllowProjectScope is whether project-wide links may be minted at all.
	AllowProjectScope bool
}

// RoleSeesDrafts reports whether a Studio role is on the visibility list.
//
// The record's creator always sees their own drafts and is not on it, which is
// why this is not the whole admission test: it answers "does this role see every
// draft", and a caller who fails it may still be the creator of the one in hand.
func (s PublishingSettings) RoleSeesDrafts(role string) bool {
	return slices.Contains(s.DraftVisibility, strings.TrimSpace(role))
}

type adminConfigPublishing struct {
	Publishing *struct {
		DraftVisibility          []string `json:"draftVisibility"`
		PreviewDefaultTTL        int      `json:"previewDefaultTtl"`
		PreviewMaxRecordTTL      int      `json:"previewMaxRecordTtl"`
		PreviewMaxProjectTTL     int      `json:"previewMaxProjectTtl"`
		PreviewAllowProjectScope *bool    `json:"previewAllowProjectScope"`
	} `json:"publishing"`
}

// Defaults, matching the CLI's. Stated here as well because this process reads a
// file that a *previous* push wrote: an older engine sends a `publishing` block
// with fewer keys, and a zero ceiling would mint links that have already expired.
const (
	defaultPreviewTTL    = 900
	defaultMaxRecordTTL  = 604800
	defaultMaxProjectTTL = 86400
)

// PublishingSettingsFromConfigFile reads the block the engine wrote.
//
// Returns errNoPublishing when the file is absent, unreadable, or carries no
// `publishing` block, which is the ordinary state of a project with no versioned
// model. Callers turn that into "there are no drafts to preview" rather than an
// error, because it is not one.
func PublishingSettingsFromConfigFile(path string) (PublishingSettings, error) {
	data, err := ReadAdminConfigFile(path)
	if err != nil {
		return PublishingSettings{}, errNoPublishing
	}

	var cfg adminConfigPublishing
	if err := json.Unmarshal(data, &cfg); err != nil || cfg.Publishing == nil {
		return PublishingSettings{}, errNoPublishing
	}

	settings := PublishingSettings{
		DraftVisibility: cfg.Publishing.DraftVisibility,
		DefaultTTL:      positiveOr(cfg.Publishing.PreviewDefaultTTL, defaultPreviewTTL),
		MaxRecordTTL:    positiveOr(cfg.Publishing.PreviewMaxRecordTTL, defaultMaxRecordTTL),
		MaxProjectTTL:   positiveOr(cfg.Publishing.PreviewMaxProjectTTL, defaultMaxProjectTTL),
		AllowProjectScope: cfg.Publishing.PreviewAllowProjectScope == nil ||
			*cfg.Publishing.PreviewAllowProjectScope,
	}
	return settings, nil
}

func positiveOr(value, fallback int) int {
	if value >= 1 {
		return value
	}
	return fallback
}
