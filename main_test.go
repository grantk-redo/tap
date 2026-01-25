package main

import (
	"testing"
)

func TestParseServiceCommand(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantService string
		wantCommand string
		wantArgs    []string
		wantErr     bool
	}{
		{
			name:        "simple command",
			input:       "api:./server",
			wantService: "api",
			wantCommand: "./server",
			wantArgs:    []string{},
			wantErr:     false,
		},
		{
			name:        "command with arguments",
			input:       "worker:python worker.py --queue=default",
			wantService: "worker",
			wantCommand: "python",
			wantArgs:    []string{"worker.py", "--queue=default"},
			wantErr:     false,
		},
		{
			name:        "command with multiple arguments",
			input:       "db:docker compose up postgres",
			wantService: "db",
			wantCommand: "docker",
			wantArgs:    []string{"compose", "up", "postgres"},
			wantErr:     false,
		},
		{
			name:        "command with path containing colon",
			input:       "api:./cmd/api:main",
			wantService: "api",
			wantCommand: "./cmd/api:main",
			wantArgs:    []string{},
			wantErr:     false,
		},
		{
			name:        "service with dashes",
			input:       "my-api:./start.sh",
			wantService: "my-api",
			wantCommand: "./start.sh",
			wantArgs:    []string{},
			wantErr:     false,
		},
		{
			name:        "service with underscores",
			input:       "my_worker:npm run dev",
			wantService: "my_worker",
			wantCommand: "npm",
			wantArgs:    []string{"run", "dev"},
			wantErr:     false,
		},
		{
			name:        "command with extra spaces",
			input:       "api:  ./server  --port 8080  ",
			wantService: "api",
			wantCommand: "./server",
			wantArgs:    []string{"--port", "8080"},
			wantErr:     false,
		},
		{
			name:    "missing colon",
			input:   "api./server",
			wantErr: true,
		},
		{
			name:    "empty service",
			input:   ":./server",
			wantErr: true,
		},
		{
			name:    "empty command",
			input:   "api:",
			wantErr: true,
		},
		{
			name:    "whitespace only command",
			input:   "api:   ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseServiceCommand(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseServiceCommand(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if got.service != tt.wantService {
				t.Errorf("service = %q, want %q", got.service, tt.wantService)
			}
			if got.command != tt.wantCommand {
				t.Errorf("command = %q, want %q", got.command, tt.wantCommand)
			}
			if len(got.args) != len(tt.wantArgs) {
				t.Errorf("args = %v, want %v", got.args, tt.wantArgs)
			} else {
				for i := range got.args {
					if got.args[i] != tt.wantArgs[i] {
						t.Errorf("args[%d] = %q, want %q", i, got.args[i], tt.wantArgs[i])
					}
				}
			}
		})
	}
}
