package main

import (
	"encoding/hex"
	"fmt"

	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	statusCommand = app.Command("status", "Display status information.")
)

func init() {
	statusCommand.Action(runRepositoryInfoCommand)
}

func runRepositoryInfoCommand(context *kingpin.ParseContext) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	fmt.Println("Vault:")
	fmt.Println("  Address:   ", v.Storage.Configuration().Config.ToURL())
	fmt.Println("  ID:        ", hex.EncodeToString(v.Format.UniqueID))
	fmt.Println("  Encryption:", v.Format.Encryption)
	fmt.Println("  Checksum:  ", v.Format.Checksum)
	fmt.Println("  Master Key:", hex.EncodeToString(v.MasterKey))

	rc, err := v.RepositoryConfig()
	if err != nil {
		return err
	}

	fmt.Println("Repository:")
	fmt.Println("  Address:         ", rc.Storage.Config.ToURL())
	fmt.Println("  Version:         ", rc.Format.Version)
	fmt.Println("  Secret:          ", len(rc.Format.Secret), "bytes")
	fmt.Println("  ID Format:       ", rc.Format.ObjectFormat)
	fmt.Println("  Blob Size:       ", rc.Format.MaxBlobSize)
	fmt.Println("  Inline Blob Size:", rc.Format.MaxInlineBlobSize)

	return nil
}