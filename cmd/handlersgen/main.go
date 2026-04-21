package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/FlorinBalint/gocrud/handlersgen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

func main() {
	messageName := flag.String("message", "", "Full protobuf message name (e.g., library.v1.Book) (required)")
	outDir := flag.String("out_dir", "", "Output directory for generated .go files (required)")
	importPath := flag.String("import_path", "", "Go import path for the entity proto package (optional override)")
	flag.Parse()

	if *messageName == "" || *outDir == "" || flag.NArg() == 0 {
		log.Fatalf("Usage: handlersgen -message <name> -out_dir <dir> [-import_path <path>] <descriptor_set1> [descriptor_set2...]")
	}

	var mergedFds descriptorpb.FileDescriptorSet
	for _, dsPath := range flag.Args() {
		b, err := os.ReadFile(dsPath)
		if err != nil {
			log.Fatalf("reading descriptor set %q: %v", dsPath, err)
		}
		var fds descriptorpb.FileDescriptorSet
		if err := proto.Unmarshal(b, &fds); err != nil {
			log.Fatalf("unmarshaling descriptor set %q: %v", dsPath, err)
		}
		mergedFds.File = append(mergedFds.File, fds.File...)
	}

	files, err := protodesc.NewFiles(&mergedFds)
	if err != nil {
		log.Fatalf("building registry from descriptor sets: %v", err)
	}

	desc, err := files.FindDescriptorByName(protoreflect.FullName(*messageName))
	if err != nil {
		log.Fatalf("finding message %q: %v", *messageName, err)
	}

	msgDesc, ok := desc.(protoreflect.MessageDescriptor)
	if !ok {
		log.Fatalf("descriptor %q is a %T, not a MessageDescriptor", *messageName, desc)
	}

	generated, err := handlersgen.GenerateHandlers(msgDesc, *importPath)
	if err != nil {
		log.Fatalf("generating handlers: %v", err)
	}

	for _, f := range generated {
		outPath := filepath.Join(*outDir, f.Filename)
		if err := os.WriteFile(outPath, []byte(f.Content), 0644); err != nil {
			log.Fatalf("writing %s: %v", outPath, err)
		}
	}
}
