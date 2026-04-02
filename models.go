package main

type Email struct {
	UID               string
	Subject           string
	Sender            string
	Date              string
	BodySnipet        string
	Category          string
	Importance        int
	Summary           string
	ActionRequired    bool
	ActionDescription string
	Deadline          string
}
type Clssification struct {
	Index             int
	Category          string
	Importance        int
	Summary           string
	ActionRequired    bool
	ActionDescription string
	Deadline          string
}

type Stats struct {
	Total          int
	Important      int
	ActionRequired int
	Categories     map[string]int
}
