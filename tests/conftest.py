"""Shared pytest configuration."""
import warnings


def pytest_configure(config):
    # SQLite connections may be garbage-collected between tests; suppress the noise.
    warnings.filterwarnings(
        "ignore",
        message="unclosed database",
        category=ResourceWarning,
    )
