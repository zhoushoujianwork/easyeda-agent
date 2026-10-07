'use strict';

// DSH installers resolve the package entry even for a configuration-only bundle.
// MCP and Skill registration belongs to cordis.patch.yml; loading this entry
// must not start another server or register duplicate providers.
exports.name = 'easyeda-agent-dsh';
exports.apply = function apply() {};
