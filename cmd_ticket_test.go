package main

import "testing"

// TestTicketFieldOptionsExpectationWarnings covers every branch of the single
// expectations method that replaced the old preferClassification/preferContact
// pair: expectations only apply to deliberate creations, classification yields
// exactly one warning (missing issue type vs missing sub-issue type), and the
// contact expectation is independent of classification.
func TestTicketFieldOptionsExpectationWarnings(t *testing.T) {
	tests := []struct {
		name string
		opts ticketFieldOptions
		want []string // substrings that must each appear; len also guards against extras
	}{
		{
			name: "not creating yields nothing",
			opts: ticketFieldOptions{},
			want: nil,
		},
		{
			name: "creating with no classification flags both classification and contact",
			opts: ticketFieldOptions{creating: true},
			want: []string{"most new tickets should be classified", "contactID is unset"},
		},
		{
			name: "issue type set but sub-issue missing flags sub-issue and contact",
			opts: ticketFieldOptions{creating: true, issueType: 10},
			want: []string{"subIssueType is unset", "contactID is unset"},
		},
		{
			name: "fully classified but no contact flags only contact",
			opts: ticketFieldOptions{creating: true, issueType: 10, subIssueType: 200},
			want: []string{"contactID is unset"},
		},
		{
			name: "fully classified with contact flags nothing",
			opts: ticketFieldOptions{creating: true, issueType: 10, subIssueType: 200, contactID: 300},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.opts.expectationWarnings()
			if len(tt.want) == 0 {
				if len(got) != 0 {
					t.Fatalf("expected no warnings, got %v", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("expected %d warnings, got %d: %v", len(tt.want), len(got), got)
			}
			for _, w := range tt.want {
				if !warningsContain(got, w) {
					t.Errorf("missing warning %q in %v", w, got)
				}
			}
		})
	}
}
