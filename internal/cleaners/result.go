package cleaners

// CleanupResult reports the outcome of a single cleaner run.
type CleanupResult struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Saved      *int64 `json:"saved"`
	SavedHuman string `json:"saved_human"`
	Detail     string `json:"detail"`
	Command    string `json:"command"`
}
