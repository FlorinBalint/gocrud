load("@rules_proto//proto:defs.bzl", "ProtoInfo")

def _grpc_crud_service_impl(ctx):
    out_file = ctx.actions.declare_file(ctx.attr.out)

    proto_info = ctx.attr.proto[ProtoInfo]
    descriptor_sets = proto_info.transitive_descriptor_sets.to_list()

    args = ctx.actions.args()
    args.add("-message", ctx.attr.message)
    args.add("-out", out_file.path)
    args.add_all(descriptor_sets)

    ctx.actions.run(
        inputs = descriptor_sets,
        outputs = [out_file],
        arguments = [args],
        executable = ctx.executable._generator,
        mnemonic = "EntityGen",
        progress_message = "Generating gRPC CRUD service for %s" % ctx.attr.message,
    )

    return [DefaultInfo(files = depset([out_file]))]

grpc_crud_service_gen = rule(
    implementation = _grpc_crud_service_impl,
    attrs = {
        "message": attr.string(mandatory = True),
        "out": attr.string(mandatory = True),
        "proto": attr.label(providers = [ProtoInfo], mandatory = True),
        "_generator": attr.label(
            default = Label("//cmd/entitygen"),
            executable = True,
            cfg = "exec",
        ),
    },
)

def grpc_crud_service(name, message, proto, out = None, **kwargs):
    if not out:
        out = name + ".proto"

    grpc_crud_service_gen(
        name = name + "_gen",
        message = message,
        proto = proto,
        out = out,
    )

    native.proto_library(
        name = name,
        srcs = [out],
        deps = [proto],
        **kwargs
    )
