package hubclient

import "time"

const ContractVersion = "hub-share/v1"

const (
	PairingStatePending               = "pending"
	PairingStateRetrying              = "retrying"
	PairingStatePaired                = "paired"
	PairingStateDeclined              = "declined"
	PairingStateExpired               = "expired"
	PairingStateCredentialStoreFailed = "credential_store_failed"
)

type Destination struct {
	WorkspaceID                 string `json:"workspaceId"`
	WorkspaceName               string `json:"workspaceName"`
	CurrentMemberCount          int    `json:"currentMemberCount"`
	ResultingMemberCount        int    `json:"resultingMemberCount"`
	CurrentApprovedSessionCount int    `json:"currentApprovedSessionCount"`
	HistoryDisclosure           string `json:"historyDisclosure"`
	CredentialState             string `json:"credentialState"`
	AudienceVersion             string `json:"audienceVersion"`
}

type DestinationResult struct {
	ContractVersion string       `json:"contractVersion"`
	State           string       `json:"state"`
	Destination     *Destination `json:"destination,omitempty"`
	Configured      bool         `json:"configured"`
	HubURL          string       `json:"hubUrl,omitempty"`
}

type PairingResult struct {
	State                   string    `json:"state"`
	PairingID               string    `json:"pairingId,omitempty"`
	UserCode                string    `json:"userCode,omitempty"`
	VerificationURI         string    `json:"verificationUri,omitempty"`
	VerificationURIComplete string    `json:"verificationUriComplete,omitempty"`
	ExpiresAt               time.Time `json:"expiresAt,omitempty"`
	IntervalSeconds         int       `json:"intervalSeconds,omitempty"`
}

// CapabilityScaleImport is advertised by Local in CheckIn.capabilities and by
// the Hub in CheckInResponse.capabilities (scale-contracts/v1). Local sends
// queue.inventory only after the Hub has advertised it, because the Hub's
// check-in decoder rejects unknown fields.
const CapabilityScaleImport = "scale-import/v1"

// DeviceInventory mirrors scale-contracts/v1 DeviceInventory: the stat-only
// result of scanning the vendor roots. It is content-free by construction:
// counts, bytes, agent names and one timestamp.
type DeviceInventory struct {
	ScannedAt      string           `json:"scannedAt"`
	DurationMs     int64            `json:"durationMs"`
	Files          int64            `json:"files"`
	Bytes          int64            `json:"bytes"`
	LargestBytes   int64            `json:"largestBytes"`
	FilesOver10MiB int64            `json:"filesOver10MiB"`
	Agents         []InventoryAgent `json:"agents"`
	Windows        InventoryWindows `json:"windows"`
}

type InventoryAgent struct {
	Agent string `json:"agent"`
	Files int64  `json:"files"`
	Bytes int64  `json:"bytes"`
}

type InventoryWindow struct {
	Sessions int64 `json:"sessions"`
	Bytes    int64 `json:"bytes"`
}

// InventoryWindows are cumulative activity buckets: h24 ⊆ d3 ⊆ d7 ⊆ d10 ⊆ d30 ⊆ d60 ⊆ all.
type InventoryWindows struct {
	H24 InventoryWindow `json:"h24"`
	D3  InventoryWindow `json:"d3"`
	D7  InventoryWindow `json:"d7"`
	D10 InventoryWindow `json:"d10"`
	D30 InventoryWindow `json:"d30"`
	D60 InventoryWindow `json:"d60"`
	All InventoryWindow `json:"all"`
}
