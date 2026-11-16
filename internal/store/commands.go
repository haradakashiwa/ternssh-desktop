package store

// QuickCommand is a command of the 本机 tab, sent to a terminal with a
// click.
type QuickCommand struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Command string `json:"command"`
}

// QuickCommands returns the quick commands of this machine.
func (s *Store) QuickCommands() []QuickCommand {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]QuickCommand(nil), s.d.QuickCommands...)
}

// PutQuickCommand adds a quick command, or replaces the one with its ID.
func (s *Store) PutQuickCommand(q QuickCommand) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.d.QuickCommands {
		if s.d.QuickCommands[i].ID == q.ID {
			s.d.QuickCommands[i] = q
			return s.save()
		}
	}
	s.d.QuickCommands = append(s.d.QuickCommands, q)
	return s.save()
}

// DeleteQuickCommand removes a quick command.
func (s *Store) DeleteQuickCommand(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmds := s.d.QuickCommands[:0]
	for _, q := range s.d.QuickCommands {
		if q.ID != id {
			cmds = append(cmds, q)
		}
	}
	s.d.QuickCommands = cmds
	return s.save()
}
