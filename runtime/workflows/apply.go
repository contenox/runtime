package workflows

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/contenox/runtime/runtime/vfs"
)

type stagedDocument struct {
	change               Change
	parent               *os.Root
	leaf, next, original string
	installed            bool
}

type stagedProposal []*stagedDocument

func (batch stagedProposal) close() {
	for _, doc := range batch {
		if doc.next != "" {
			doc.parent.Remove(doc.next)
		}
		if doc.original != "" {
			doc.parent.Remove(doc.original)
		}
		doc.parent.Close()
	}
}

// Prepare every replacement and its rollback copy before any destination changes.
func stageProposal(root *os.Root, changes []Change, resuming bool) (stagedProposal, error) {
	var batch stagedProposal
	for _, c := range changes {
		doc, err := stageDocument(root, c, resuming)
		if err != nil {
			batch.close()
			return nil, err
		}
		batch = append(batch, doc)
	}
	return batch, nil
}

func stageDocument(root *os.Root, c Change, resuming bool) (*stagedDocument, error) {
	name, err := documentPath(c.Path)
	if err != nil {
		return nil, err
	}
	parent, leaf, err := vfs.OpenFileParent(root, name, false)
	if err != nil {
		return nil, err
	}
	doc := &stagedDocument{change: c, parent: parent, leaf: leaf}
	before, hash, err := doc.current()
	if err == nil && (resuming || hash == c.BeforeHash) && hash == digest(c.bytes()) {
		return doc, nil
	}
	if err == nil && hash != c.BeforeHash {
		err = doc.conflict()
	}
	if err == nil {
		doc.next, err = stageFile(parent, leaf, c.bytes())
	}
	if err == nil && hash != "" {
		doc.original, err = stageFile(parent, leaf, before)
	}
	if err != nil {
		stagedProposal{doc}.close()
		return nil, err
	}
	return doc, nil
}

func (doc *stagedDocument) current() ([]byte, string, error) {
	data, err := readDocument(doc.parent, doc.leaf, maxPDF)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return data, digest(data), nil
}

func (doc *stagedDocument) conflict() error {
	return fmt.Errorf("%w: %s changed since the run; review a new proposal", ErrConflict, doc.change.Path)
}

func (batch stagedProposal) apply() error {
	for _, doc := range batch {
		_, hash, err := doc.current()
		if err != nil {
			return err
		}
		if doc.next == "" {
			if hash != digest(doc.change.bytes()) {
				return doc.conflict()
			}
			continue
		}
		if hash != doc.change.BeforeHash {
			return doc.conflict()
		}
		if err := doc.parent.Rename(doc.next, doc.leaf); err != nil {
			return err
		}
		doc.next = ""
		doc.installed = true
	}
	return nil
}

func (batch stagedProposal) undo() error {
	var result error
	for i := len(batch) - 1; i >= 0; i-- {
		doc := batch[i]
		if !doc.installed {
			continue
		}
		_, hash, err := doc.current()
		if err == nil && hash != digest(doc.change.bytes()) {
			err = doc.conflict()
		}
		if err == nil {
			if doc.original == "" {
				err = doc.parent.Remove(doc.leaf)
			} else {
				err = doc.parent.Rename(doc.original, doc.leaf)
			}
		}
		result = errors.Join(result, err)
	}
	return result
}
