package domain

type CodeInterpreterContainerExpirationPolicy struct {
	Anchor  string `json:"anchor"`
	Minutes int    `json:"minutes"`
}

type ListCodeInterpreterSessionsQuery struct {
	After string
	Limit int
	Order string
	Name  string
	Owner string
}

type CodeInterpreterSessionPage struct {
	Sessions []CodeInterpreterSession
	HasMore  bool
}

type CodeInterpreterContainerFile struct {
	ID                string
	ContainerID       string
	BackingFileID     string
	DeleteBackingFile bool
	Path              string
	Source            string
	Bytes             int64
	CreatedAt         int64
}

type ListCodeInterpreterContainerFilesQuery struct {
	ContainerID string
	After       string
	Limit       int
	Order       string
}

type CodeInterpreterContainerFilePage struct {
	Files   []CodeInterpreterContainerFile
	HasMore bool
}
