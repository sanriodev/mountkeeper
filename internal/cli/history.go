package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/history"
	"github.com/sanriodev/mountkeeper/internal/share"
)

func newHistoryCmd() *cobra.Command {
	var limit int
	var asJSON, clear bool
	cmd := &cobra.Command{
		Use:   "history [share]",
		Short: "Show when shares were connected, disconnected, or failed to connect",
		Long: `Show the connection history, newest entries last: what was connected or
disconnected, whether you did it or the background agent, and why an
attempt failed. The history is kept locally and never contains passwords.`,
		Example: `  mountkeeper history
  mountkeeper history projects -n 5
  mountkeeper history --clear`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeOneShare,
		RunE: func(cmd *cobra.Command, args []string) error {
			if clear {
				if err := history.Clear(); err != nil {
					return err
				}
				success("History cleared")
				return nil
			}
			id := ""
			if len(args) == 1 {
				cfg, err := config.Load()
				if err != nil {
					return err
				}
				s, err := cfg.Get(args[0])
				if err != nil {
					return err
				}
				id = s.ID
			}
			events, err := history.Read(id, limit)
			if err != nil {
				return err
			}
			if asJSON {
				if events == nil {
					events = []history.Event{}
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(events)
			}
			if len(events) == 0 {
				fmt.Println(dimStyle.Render("Nothing recorded yet."))
				return nil
			}
			var rows [][]string
			for _, e := range events {
				action := dimStyle.Render(e.Action)
				switch e.Action {
				case history.Connected:
					action = okStyle.Render(e.Action)
				case history.Failed:
					action = errStyle.Render(e.Action)
				}
				by := "by you"
				if e.Trigger == history.Auto {
					by = "automatically"
				}
				rows = append(rows, []string{
					dimStyle.Render(e.Time.Local().Format("2006-01-02 15:04")),
					boldStyle.Render(e.Share), action, dimStyle.Render(by),
					dimStyle.Render(share.CollapsePath(e.Detail)),
				})
			}
			table(rows)
			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "number", "n", 20, "show the newest N entries (0 = all)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	cmd.Flags().BoolVar(&clear, "clear", false, "delete the history")
	return cmd
}
