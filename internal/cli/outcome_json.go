package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/outcomes"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

const maxOutcomeJSONBytes = 1 << 20

// newOutcomeCheckJSONCmd records a narrow verifier result without retaining
// either the response body or the expected value in TokenOps evidence.
func newOutcomeCheckJSONCmd() *cobra.Command {
	var filePath, pointer, expected, decisionID, dbPath string
	cmd := &cobra.Command{
		Use: "check-json <execution-id>", Short: "Verify a scalar in a local JSON response", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			matched, err := checkJSONScalar(filePath, pointer, expected)
			if err != nil {
				return err
			}
			result := eventschema.OutcomeNotAchieved
			caveat := "the selected JSON scalar did not match; response content and the expected value were not retained"
			if matched {
				result = eventschema.OutcomeAchieved
				caveat = "the selected JSON scalar matched; response content and the expected value were not retained"
			}
			at := time.Now().UTC()
			env := outcomes.Event(outcomes.Record{
				ExecutionID: args[0], DecisionID: decisionID, Result: result,
				Assessment: eventschema.OutcomeVerification, Caveat: caveat, At: at,
				Evidence: []eventschema.EvidenceRef{{
					Kind: "json_equality_verification", Source: "local_file", ObservedAt: at,
					Confidence: 1, Scope: "json_pointer:" + pointer,
				}},
			})
			store, closeStore, err := openControlStore(cmd, dbPath)
			if err != nil {
				return err
			}
			defer closeStore()
			if env.Correlation.Decision == "" {
				history, err := store.Query(cmd.Context(), sqlite.Filter{Execution: args[0], Limit: 10_000})
				if err != nil {
					return err
				}
				for _, candidate := range history {
					if candidate.Correlation.Decision != "" {
						env.Correlation.Decision = candidate.Correlation.Decision
						break
					}
				}
			}
			if env.Correlation.Decision != "" {
				history, err := store.Query(cmd.Context(), sqlite.Filter{Decision: env.Correlation.Decision, Limit: 10_000})
				if err != nil {
					return err
				}
				outcomes.CorrelateDecisionLifecycle(env, history)
			}
			if err := store.Append(cmd.Context(), env); err != nil {
				return err
			}
			return writeControlJSON(cmd, map[string]any{
				"event_id": env.ID, "execution_id": args[0], "result": result,
				"assessment": eventschema.OutcomeVerification, "scope": "json_pointer:" + pointer,
			})
		},
	}
	cmd.Flags().StringVar(&filePath, "file", "", "path to the local JSON response")
	cmd.Flags().StringVar(&pointer, "pointer", "", "RFC 6901 JSON Pointer selecting a scalar")
	cmd.Flags().StringVar(&expected, "equals", "", "expected scalar value")
	cmd.Flags().StringVar(&decisionID, "decision", "", "decision id this outcome evaluates")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db")
	_ = cmd.MarkFlagRequired("file")
	_ = cmd.MarkFlagRequired("pointer")
	_ = cmd.MarkFlagRequired("equals")
	return cmd
}

func checkJSONScalar(filePath, pointer, expected string) (bool, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	decoder := json.NewDecoder(io.LimitReader(f, maxOutcomeJSONBytes+1))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return false, fmt.Errorf("decode response JSON: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return false, fmt.Errorf("decode response JSON: trailing content")
		}
		return false, fmt.Errorf("decode response JSON: %w", err)
	}
	value, err := resolveJSONPointer(document, pointer)
	if err != nil {
		return false, err
	}
	scalar, err := jsonScalarString(value)
	if err != nil {
		return false, err
	}
	return scalar == expected, nil
}

func resolveJSONPointer(value any, pointer string) (any, error) {
	if pointer == "" {
		return value, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("JSON Pointer must be empty or start with /")
	}
	current := value
	for _, raw := range strings.Split(pointer[1:], "/") {
		part := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		switch node := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = node[part]
			if !ok {
				return nil, fmt.Errorf("JSON Pointer component %q was not found", part)
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(node) {
				return nil, fmt.Errorf("JSON Pointer component %q is not a valid array index", part)
			}
			current = node[index]
		default:
			return nil, fmt.Errorf("JSON Pointer component %q traverses a scalar", part)
		}
	}
	return current, nil
}

func jsonScalarString(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case bool:
		return strconv.FormatBool(v), nil
	case nil:
		return "null", nil
	default:
		return "", fmt.Errorf("selected JSON value must be a scalar")
	}
}
