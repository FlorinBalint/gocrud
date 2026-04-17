package main

import (
	"flag"
	"log"
	"os"

	"github.com/FlorinBalint/gocrud/entitygen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

func main() {
	messageName := flag.String("message", "", "Full protobuf message name (e.g., library.v1.Book) (required)")
	out := flag.String("out", "", "Output .proto file path (required)")
	flag.Parse()

	if *messageName == "" || *out == "" || flag.NArg() == 0 {
		log.Fatalf("Usage: entitygen -message <name> -out <file> <descriptor_set1> [descriptor_set2...]")
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

	generated, err := entitygen.GenerateServiceProto(msgDesc)
	if err != nil {
		log.Fatalf("generating service proto: %v", err)
	}

	if err := os.WriteFile(*out, []byte(generated), 0644); err != nil {
		log.Fatalf("writing output file: %v", err)
	}
}
