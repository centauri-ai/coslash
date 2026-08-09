package sessiondetail

import (
	"errors"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

var (
	ErrNotFound            = errors.New("canvas session transcript not found")
	ErrUnknownAgent        = errors.New("unsupported canvas session agent")
	ErrIdentityMismatch    = errors.New("canvas session identity does not match transcript")
	ErrTranscriptTooLarge  = errors.New("canvas session transcript exceeds size limit")
	ErrMalformedTranscript = errors.New("canvas session transcript is malformed")
	ErrProjectionTooLarge  = errors.New("canvas session detail exceeds projection limit")
)

type Decision struct {
	Question string  `json:"question"`
	Answer   *string `json:"answer"`
}

type Turn struct {
	Index     int        `json:"index"`
	Prompt    string     `json:"prompt"`
	PlanText  *string    `json:"planText"`
	Todos     []Todo     `json:"todos"`
	ToolUses  int        `json:"toolUses"`
	Errors    int        `json:"errors"`
	Decisions []Decision `json:"decisions"`
	FileEdits []string   `json:"fileEdits"`
}

type Todo struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type FileEdit struct {
	Path      string   `json:"path"`
	Additions int      `json:"adds"`
	Deletions int      `json:"dels"`
	Edits     int      `json:"edits"`
	IsNew     bool     `json:"isNew"`
	Hunks     []string `json:"hunks"`
}

type ContextSegment struct {
	StartLine int    `json:"startLine"`
	Content   string `json:"content"`
}

type ContextFile struct {
	Path            string           `json:"path"`
	Partial         bool             `json:"partial"`
	TotalLines      *int             `json:"totalLines"`
	CapturedContent bool             `json:"capturedContent"`
	CombinedReadID  *string          `json:"combinedReadId"`
	Segments        []ContextSegment `json:"segments"`
}

type ContextReadGroup struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Output  string `json:"output"`
}

type DeferredContext struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

type TriggeredContext struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Calls int    `json:"calls"`
}

// Detail embeds the existing coSlash session projection so Canvas consumers
// receive the same names, synthesis, tokens, status facts, and composite
// identity. The fields below are the expensive transcript-only additions.
type Detail struct {
	session.Session
	TurnLog           []Turn             `json:"turnLog"`
	FileEdits         []FileEdit         `json:"fileEdits"`
	ContextFiles      []ContextFile      `json:"contextFiles"`
	ContextReadGroups []ContextReadGroup `json:"contextReadGroups"`
	DeferredContext   []DeferredContext  `json:"deferredContext"`
	TriggeredContext  []TriggeredContext `json:"triggeredContext"`
}

type heavyDetail struct {
	turns          map[int]*Turn
	turnOrder      []int
	fileEdits      map[string]*FileEdit
	fileOrder      []string
	contextFiles   map[string]*ContextFile
	contextOrder   []string
	readGroups     []ContextReadGroup
	deferred       []DeferredContext
	triggered      map[string]*TriggeredContext
	triggeredOrder []string
}

func newHeavyDetail() *heavyDetail {
	return &heavyDetail{
		turns:        map[int]*Turn{},
		fileEdits:    map[string]*FileEdit{},
		contextFiles: map[string]*ContextFile{},
		triggered:    map[string]*TriggeredContext{},
	}
}
