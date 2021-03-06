package model

// generated by gen-qbec-swagger from internal/model/swagger.yaml at 2019-02-23 05:19:18.686016829 +0000 UTC
// Do NOT edit this file by hand

var swaggerJSON = `
{
    "definitions": {
        "qbec.io.v1alpha1.App": {
            "additionalProperties": false,
            "description": "The list of all components for the app is derived as all the supported (jsonnet, json, yaml) files in the components subdirectory.",
            "properties": {
                "apiVersion": {
                    "description": "requested API version",
                    "type": "string"
                },
                "kind": {
                    "description": "object kind",
                    "pattern": "^App$",
                    "type": "string"
                },
                "metadata": {
                    "$ref": "#/definitions/qbec.io.v1alpha1.AppMeta"
                },
                "spec": {
                    "$ref": "#/definitions/qbec.io.v1alpha1.AppSpec"
                }
            },
            "required": [
                "kind",
                "apiVersion",
                "metadata",
                "spec"
            ],
            "title": "QbecApp is a set of components that can be applied to multiple environments with tweaked runtime configurations.",
            "type": "object"
        },
        "qbec.io.v1alpha1.AppMeta": {
            "additionalProperties": false,
            "properties": {
                "name": {
                    "pattern": "^(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])$",
                    "type": "string"
                }
            },
            "required": [
                "name"
            ],
            "title": "AppMeta is the simplified metadata object for a qbec app.",
            "type": "object"
        },
        "qbec.io.v1alpha1.AppSpec": {
            "additionalProperties": false,
            "properties": {
                "componentsDir": {
                    "description": "directory containing component files, default to components/",
                    "type": "string"
                },
                "environments": {
                    "additionalProperties": {
                        "$ref": "#/definitions/qbec.io.v1alpha1.Environment"
                    },
                    "description": "set of environments for the app",
                    "minProperties": 1,
                    "type": "object"
                },
                "excludes": {
                    "description": "list of components to exclude by default for every environment",
                    "items": {
                        "type": "string"
                    },
                    "type": "array"
                },
                "libPaths": {
                    "description": "list of library paths to add to the jsonnet VM at evaluation",
                    "items": {
                        "type": "string"
                    },
                    "type": "array"
                },
                "paramsFile": {
                    "description": "standard file containing parameters for all environments returning correct values based on qbec.io/env external\nvariable, defaults to params.libsonnet",
                    "type": "string"
                }
            },
            "required": [
                "environments"
            ],
            "title": "AppSpec is the user-supplied configuration of the qbec app.",
            "type": "object"
        },
        "qbec.io.v1alpha1.Environment": {
            "additionalProperties": false,
            "properties": {
                "defaultNamespace": {
                    "type": "string"
                },
                "excludes": {
                    "items": {
                        "type": "string"
                    },
                    "type": "array"
                },
                "includes": {
                    "items": {
                        "type": "string"
                    },
                    "type": "array"
                },
                "server": {
                    "type": "string"
                }
            },
            "title": "Environment points to a specific destination and has its own set of runtime parameters.",
            "type": "object"
        }
    },
    "paths": {},
    "swagger": "2.0"
}
`
