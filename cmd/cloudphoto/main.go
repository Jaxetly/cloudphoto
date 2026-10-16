package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"cloudphoto/internal/archive"
	"cloudphoto/internal/cli"
	"cloudphoto/internal/storage"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(cli.Run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, execute))
}

func execute(ctx context.Context, config cli.Config, args cli.Args) ([]string, error) {
	a := archive.Archive{Store: storage.New(config.Bucket, config.AccessKey, config.SecretKey)}
	switch args.Command {
	case "list":
		if args.Album == "" {
			return a.Albums(ctx)
		}
		photos, err := a.Photos(ctx, args.Album)
		if err != nil {
			return nil, err
		}
		names := make([]string, len(photos))
		for i, p := range photos {
			names[i] = p.Name
		}
		return names, nil
	case "upload":
		return nil, a.Upload(ctx, args.Album, args.Path)
	case "download":
		return nil, a.Download(ctx, args.Album, args.Path)
	case "delete":
		return nil, a.Delete(ctx, args.Album, args.Photo)
	case "mksite":
		return nil, a.MakeSite(ctx)
	}
	return nil, cli.ErrArguments
}
