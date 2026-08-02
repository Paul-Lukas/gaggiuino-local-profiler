// Gaggiuino machine WebSocket/Protobuf message schema — reconstructed field-
// for-field from the machine's own web UI bundle (reflection-based message
// definitions the bundle ships for its own protobuf-ts runtime) and verified
// live against a real machine: GetProfileDict, GetProfileById, CreateNewProfile,
// UpdateProfile and DeleteProfile were all round-tripped successfully.
//
// Updating or deleting a profile only works over this binary WebSocket
// protocol (`ws://<host>/ws`) — no REST equivalent exists for either
// (live-verified, #580). Reads (GET /api/profiles/all, the list) exist as
// REST and are used elsewhere in this app (routes/system.js). Profile
// *detail* also gained a REST read on newer firmware (build 7889b7d+,
// GET /api/profile/:id, same JSON shape as ProfileDto below) — GLP tries
// that first and falls back to this module's getProfileById on older
// firmware (see lib/machines/gaggiuino/adapter.js's getProfile()). Newer
// firmware also gained a REST *create* (POST /api/profile, create-only —
// an `id` in the body is ignored, always minting a new one, #580), tried
// first by this module's createProfile() equivalent in the adapter. This
// module remains required for update/delete on every firmware version, and
// as the create/detail-read fallback for older firmware.
const { MessageType } = require('@protobuf-ts/runtime');

// ── Enums ──
const PhaseTypeDto = { 0: 'FLOW', FLOW: 0, 1: 'PRESSURE', PRESSURE: 1, 2: 'MANUAL', MANUAL: 2 };
const TransitionCurveDto = { 0: 'EASE_IN_OUT', EASE_IN_OUT: 0, 1: 'EASE_IN', EASE_IN: 1, 2: 'EASE_OUT', EASE_OUT: 2, 3: 'LINEAR', LINEAR: 3, 4: 'INSTANT', INSTANT: 4 };
const WebSocketResponseResultDto = { 0: 'SUCCESS', SUCCESS: 0, 1: 'ERROR', ERROR: 1 };

// ── Action codes — request (g_/c_ prefixed) and their matching server-push
// response action (d_ prefixed) are DIFFERENT strings, not the same one
// echoed back. e.g. request GetProfileDict ('g_prof_dict') is answered by a
// push whose action is 'd_prof_dict', not 'g_prof_dict'. ──
const ND = {
    GetActiveProfile: 'g_act_prof', UpdateActiveProfile: 'c_upd_act_prof',
    UpdateActiveProfileId: 'c_upd_act_prof_id', PersistActiveProfile: 'c_save_act_prof',
    GetProfileDict: 'g_prof_dict', GetProfileById: 'g_prof',
    CreateNewProfile: 'c_new_prof', UpdateProfile: 'c_upd_prof',
    DeleteProfile: 'c_del_prof', ReorderProfile: 'c_reorder_prof',
};
// Matching push-response action for each request action above.
const RESPONSE_ACTION = {
    [ND.GetProfileDict]: 'd_prof_dict',
    [ND.GetProfileById]: 'd_prof',
    [ND.CreateNewProfile]: 'd_prof_dict',
    [ND.UpdateProfile]: 'd_prof_dict',
    [ND.DeleteProfile]: 'd_prof_dict',
};

let PhaseStopConditionsDto, TransitionDto, PhaseDto, GlobalStopConditionsDto,
    BrewRecipeDto, ProfileDto, WebSocketProfileIdCommandDto,
    WebSocketMessageDto, WebSocketResponseDto, SavedProfileDto, SavedProfilesDto;

PhaseStopConditionsDto = new MessageType('PhaseStopConditionsDto', [
    { no: 1, name: 'time', kind: 'scalar', T: 13 },
    { no: 2, name: 'pressureAbove', kind: 'scalar', T: 2 },
    { no: 3, name: 'pressureBelow', kind: 'scalar', T: 2 },
    { no: 4, name: 'flowAbove', kind: 'scalar', T: 2 },
    { no: 5, name: 'flowBelow', kind: 'scalar', T: 2 },
    { no: 6, name: 'weight', kind: 'scalar', T: 2 },
    { no: 7, name: 'waterPumpedInPhase', kind: 'scalar', T: 2 },
]);

TransitionDto = new MessageType('TransitionDto', [
    { no: 1, name: 'start', kind: 'scalar', T: 2 },
    { no: 2, name: 'end', kind: 'scalar', T: 2 },
    { no: 3, name: 'curve', kind: 'enum', T: () => ['TransitionCurveDto', TransitionCurveDto] },
    { no: 4, name: 'time', kind: 'scalar', T: 13 },
    { no: 5, name: 'volume', kind: 'scalar', T: 2 },
]);

PhaseDto = new MessageType('PhaseDto', [
    { no: 1, name: 'type', kind: 'enum', T: () => ['PhaseTypeDto', PhaseTypeDto] },
    { no: 2, name: 'target', kind: 'message', T: () => TransitionDto },
    { no: 3, name: 'restriction', kind: 'scalar', T: 2 },
    { no: 4, name: 'stopConditions', kind: 'message', T: () => PhaseStopConditionsDto },
    { no: 5, name: 'waterTemperature', kind: 'scalar', T: 2 },
    { no: 6, name: 'name', kind: 'scalar', T: 9 },
    { no: 7, name: 'skip', kind: 'scalar', T: 8 },
]);

GlobalStopConditionsDto = new MessageType('GlobalStopConditionsDto', [
    { no: 1, name: 'time', kind: 'scalar', T: 13 },
    { no: 2, name: 'weight', kind: 'scalar', T: 2 },
    { no: 3, name: 'waterPumped', kind: 'scalar', T: 2 },
    { no: 4, name: 'switchToManualPressureCtrl', kind: 'scalar', T: 8 },
    { no: 5, name: 'switchToManuaFlowCtrl', kind: 'scalar', T: 8 },
]);

BrewRecipeDto = new MessageType('BrewRecipeDto', [
    { no: 1, name: 'coffeeIn', kind: 'scalar', T: 2 },
    { no: 2, name: 'coffeeOut', kind: 'scalar', T: 2 },
    { no: 3, name: 'ratio', kind: 'scalar', T: 2 },
]);

ProfileDto = new MessageType('ProfileDto', [
    { no: 1, name: 'name', kind: 'scalar', T: 9 },
    { no: 2, name: 'phases', kind: 'message', repeat: 2, T: () => PhaseDto },
    { no: 3, name: 'globalStopConditions', kind: 'message', T: () => GlobalStopConditionsDto },
    { no: 4, name: 'waterTemperature', kind: 'scalar', T: 2 },
    { no: 5, name: 'recipe', kind: 'message', T: () => BrewRecipeDto },
    { no: 6, name: 'id', kind: 'scalar', T: 13 },
]);

WebSocketProfileIdCommandDto = new MessageType('WebSocketProfileIdCommandDto', [
    { no: 1, name: 'id', kind: 'scalar', T: 13 },
]);

WebSocketMessageDto = new MessageType('WebSocketMessageDto', [
    { no: 1, name: 'action', kind: 'scalar', T: 9 },
    { no: 2, name: 'data', kind: 'scalar', T: 12 },
]);

WebSocketResponseDto = new MessageType('WebSocketResponseDto', [
    { no: 1, name: 'action', kind: 'scalar', T: 9 },
    { no: 2, name: 'result', kind: 'enum', T: () => ['WebSocketResponseResultDto', WebSocketResponseResultDto] },
    { no: 3, name: 'errorMessage', kind: 'scalar', T: 9 },
]);

SavedProfileDto = new MessageType('SavedProfileDto', [
    { no: 1, name: 'id', kind: 'scalar', T: 13 },
    { no: 2, name: 'name', kind: 'scalar', T: 9 },
]);

SavedProfilesDto = new MessageType('SavedProfilesDto', [
    { no: 1, name: 'profiles', kind: 'message', repeat: 2, T: () => SavedProfileDto },
]);

module.exports = {
    PhaseTypeDto, TransitionCurveDto, WebSocketResponseResultDto, ND, RESPONSE_ACTION,
    PhaseStopConditionsDto, TransitionDto, PhaseDto, GlobalStopConditionsDto,
    BrewRecipeDto, ProfileDto, WebSocketProfileIdCommandDto,
    WebSocketMessageDto, WebSocketResponseDto, SavedProfileDto, SavedProfilesDto,
};
