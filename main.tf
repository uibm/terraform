provider random{}
provider cloudinit{}
provider local{}

resource "random_integer" "priority" {
  min = 1
  max = 50000

}