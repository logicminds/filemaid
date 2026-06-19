"""Dev artifact cleaners registry."""
from filemaid.cleaners import docker, npm, cargo, pip, brew, xcode, review

CLEANERS = [docker, npm, cargo, pip, brew, xcode, review]
