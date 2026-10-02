// Command schema-registry demonstrates the low-code form schema registry:
// it seeds tokens, registers an evolving form schema with BACKWARD
// compatibility checking, validates an instance and prints the audit trail.
//
// Run with:
//
//	go run ./cmd/schema-registry [data-dir]
//
// When data-dir is omitted a temporary directory is used and state is
// discarded on exit. Re-run against the same directory to observe restart
// recovery.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/shikejing/schema-registry-go/audit"
	"github.com/shikejing/schema-registry-go/auth"
	"github.com/shikejing/schema-registry-go/registry"
	"github.com/shikejing/schema-registry-go/schema"
)

func main() {
	dataDir := ""
	if len(os.Args) > 1 {
		dataDir = os.Args[1]
	}
	if dataDir == "" {
		var err error
		dataDir, err = os.MkdirTemp("", "schema-registry-*")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("using temporary data dir: %s\n", dataDir)
	}

	authz := auth.NewAuthorizer()
	authz.RegisterToken("admin", "admin-user", auth.RoleAdmin)
	authz.RegisterToken("writer", "form-service", auth.RoleWriter)
	authz.RegisterToken("reader", "renderer", auth.RoleReader)

	auditLog, err := audit.NewFileLogger(filepath.Join(dataDir, "audit.jsonl"))
	if err != nil {
		log.Fatal(err)
	}

	reg, err := registry.New(dataDir, authz, auditLog)
	if err != nil {
		log.Fatal(err)
	}
	defer reg.Close()

	formV1 := &schema.Schema{
		ID:   "user-profile",
		Name: "UserProfileForm",
		Fields: []*schema.Field{
			{Name: "nickname", Type: schema.TypeString, Required: true, Min: floatPtr(1), Max: floatPtr(32)},
			{Name: "age", Type: schema.TypeInteger, Required: false, Min: floatPtr(0), Max: floatPtr(150)},
		},
	}
	v1, err := reg.Register("user-profile", formV1, "writer")
	if err != nil {
		log.Fatalf("register v1: %v", err)
	}
	fmt.Printf("registered %s v%d (fingerprint %s)\n", v1.Subject, v1.Version, v1.Fingerprint[:12])

	formV2 := &schema.Schema{
		ID:   "user-profile",
		Name: "UserProfileForm",
		Fields: []*schema.Field{
			{Name: "nickname", Type: schema.TypeString, Required: true, Min: floatPtr(1), Max: floatPtr(32)},
			{Name: "age", Type: schema.TypeInteger, Required: false, Min: floatPtr(0), Max: floatPtr(150)},
			{Name: "email", Type: schema.TypeString, Required: false}, // optional: backward compatible
		},
	}
	v2, err := reg.Register("user-profile", formV2, "writer")
	if err != nil {
		log.Fatalf("register v2: %v", err)
	}
	fmt.Printf("registered %s v%d\n", v2.Subject, v2.Version)

	instance := []byte(`{"nickname":"alice","age":30,"email":"a@example.com"}`)
	if err := reg.Validate("user-profile", 2, instance, "reader"); err != nil {
		log.Fatalf("validate: %v", err)
	}
	fmt.Println("instance valid against v2")

	// An incompatible change attempted by an unauthorized role is denied and
	// audited.
	formV3 := &schema.Schema{
		ID:   "user-profile",
		Name: "UserProfileForm",
		Fields: []*schema.Field{
			{Name: "nickname", Type: schema.TypeString, Required: true, Min: floatPtr(1), Max: floatPtr(32)},
			{Name: "age", Type: schema.TypeInteger, Required: false, Min: floatPtr(0), Max: floatPtr(150)},
			{Name: "ssn", Type: schema.TypeString, Required: true}, // new required field
		},
	}
	if _, err := reg.Register("user-profile", formV3, "writer"); err != nil {
		fmt.Printf("incompatible registration rejected: %v\n", err)
	}

	events, _ := auditLog.Query()
	fmt.Printf("audit events recorded: %d\n", len(events))
	for _, e := range events {
		fmt.Printf("  %s  %-7s  %-8s  %s@%d  %s\n", e.Time, e.Actor, e.Action, e.Subject, e.Version, e.Result)
	}
}

func floatPtr(f float64) *float64 { return &f }
