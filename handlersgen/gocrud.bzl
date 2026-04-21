load("@rules_go//go:def.bzl", "go_library")
load("@rules_proto//proto:defs.bzl", "ProtoInfo")

def _grpc_crud_handlers_impl(ctx):
    entity_name = ctx.attr.message.split(".")[-1]
    entity_lower = entity_name.lower()

    # Declare output files: types.go + one file per handler method.
    types_file = ctx.actions.declare_file("types.go")
    create_file = ctx.actions.declare_file("create_" + entity_lower + ".go")
    outputs = [types_file, create_file]

    proto_info = ctx.attr.proto[ProtoInfo]
    descriptor_sets = proto_info.transitive_descriptor_sets.to_list()

    args = ctx.actions.args()
    args.add("-message", ctx.attr.message)
    args.add("-out_dir", types_file.dirname)
    if ctx.attr.import_path:
        args.add("-import_path", ctx.attr.import_path)
    args.add_all(descriptor_sets)

    ctx.actions.run(
        inputs = descriptor_sets,
        outputs = outputs,
        arguments = [args],
        executable = ctx.executable._generator,
        mnemonic = "HandlersGen",
        progress_message = "Generating gRPC CRUD handlers for %s" % ctx.attr.message,
    )

    return [DefaultInfo(files = depset(outputs))]

grpc_crud_handlers_gen = rule(
    implementation = _grpc_crud_handlers_impl,
    attrs = {
        "message": attr.string(mandatory = True),
        "import_path": attr.string(default = ""),
        "proto": attr.label(providers = [ProtoInfo], mandatory = True),
        "_generator": attr.label(
            default = Label("//cmd/handlersgen"),
            executable = True,
            cfg = "exec",
        ),
    },
)

def grpc_crud_handlers(name, message, proto, entity_go_proto, import_path = "", deps = [], **kwargs):
    """Generate Go CRUD handler implementations from a proto entity definition.

    Args:
        name: Name for the generated go_library target.
        message: Full protobuf message name (e.g., "library.v1.Book").
        proto: Label of the proto_library containing the entity definition.
        entity_go_proto: Label of the go_proto_library for the entity.
        import_path: Go import path override for the entity proto package.
        deps: Additional deps for the go_library.
        **kwargs: Passed to go_library.
    """
    gen_name = name + "_gen"

    grpc_crud_handlers_gen(
        name = gen_name,
        message = message,
        proto = proto,
        import_path = import_path,
    )

    go_library(
        name = name,
        srcs = [":" + gen_name],
        deps = [
            entity_go_proto,
            "//proto/v1:query_go_proto",
            "//sqldialect",
            "@org_golang_google_grpc//codes",
            "@org_golang_google_grpc//status",
        ] + deps,
        **kwargs
    )
