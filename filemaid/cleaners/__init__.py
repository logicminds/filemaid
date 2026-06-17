"""Dev artifact cleaners registry."""
from filemaid.cleaners import docker, npm, cargo, pip, brew, xcode

CLEANERS = [docker, npm, cargo, pip, brew, xcode]
