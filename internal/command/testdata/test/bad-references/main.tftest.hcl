
variables {
  default = "double"
}

run "setup" {
  variables {
    input_one = var.default
    input_two = var.default
  }
}

run "test" {
  variables {
    input_one = var.notreal
    input_two = run.madeup.response
  }
}

run "finalise" {
  variables {
    input_one = var.default
    input_two = var.default
  }
}
