// Package project holds the domain types shared by the scanner, the store and
// the API: what a compose project contains, which databases it uses, and the
// container status mapping. It has no I/O.
package project

// Database is a database a project uses. Only names are kept, never values:
// Sources lists the variables or services it was found in.
type Database struct {
	Type         string   `json:"type"`         // postgresql | mysql | mongodb | sqlite
	Name         string   `json:"name"`         // database name, or the service name
	LocationKind string   `json:"locationKind"` // container | host | external | file | unknown
	Location     string   `json:"location"`     // service or container name, host name or file path
	Sources      []string `json:"sources"`
}

// Service is one service of a compose project, as the compose config reports it.
type Service struct {
	Name    string   `json:"name"`
	Image   string   `json:"image"`
	Ports   []string `json:"ports"`
	Volumes []Volume `json:"volumes"`
}

// Volume is one mount of a service.
type Volume struct {
	Type   string `json:"type"`
	Source string `json:"source"`
	Target string `json:"target"`
}

// Details is what the compose config says about a project. It is empty when
// the config could not be read.
type Details struct {
	Services []Service `json:"services"`
}
