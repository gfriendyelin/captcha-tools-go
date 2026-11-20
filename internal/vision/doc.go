// Package vision implements the image and deterministic post-processing
// stages of captcha-tools without owning ONNX sessions or touching the file
// system. Callers supply decoded model tensors and connect their ONNX binding
// to Tensor, DFINEInput, and MatchCandidate.
//
// RGB resizing follows Pillow's uint8 fixed-point algorithm. Standard-library
// JPEG and PNG codecs and the built-in drawing font are not byte-for-byte
// Pillow/libjpeg replacements; inference inputs should therefore be compared
// against golden tensors when migrating a production workload.
package vision
