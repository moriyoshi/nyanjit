# TPDE-LLVM benchmark harness. Builds TPDE from TPDE_SRC (the pinned commit
# that ../build.sh fetches) and a
# harness that drives TPDE-LLVM's aarch64 compiler. split.cpp compiles TPDE's
# own arm64/LLVMCompilerArm64.cpp verbatim so that compile_and_map() can be
# split into its two halves (compile, then JITMapperImpl::map) for timing.
cmake_minimum_required(VERSION 3.25)
project(tpdebench C CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(TPDE_INCLUDE_TESTS OFF CACHE BOOL "" FORCE)
set(TPDE_LINK_LLVM_STATIC ON CACHE BOOL "" FORCE)
set(TPDE_SRC ${CMAKE_CURRENT_SOURCE_DIR}/../../.build/tpde-src CACHE PATH "TPDE source tree")
find_package(LLVM 19.1 REQUIRED CONFIG)
add_subdirectory(${TPDE_SRC} tpde)

set(TL ${TPDE_SRC}/tpde-llvm)
add_executable(tpdebench
    bench.cpp
    split.cpp
    ${TL}/src/JITMapper.cpp
    ${TL}/src/LLVMAdaptor.cpp
)
add_dependencies(tpdebench tpde_llvm_impl) # generates encode_template_arm64.hpp
target_include_directories(tpdebench PRIVATE
    ${TL}/include ${TL}/src ${CMAKE_BINARY_DIR}/tpde/tpde-llvm)
target_include_directories(tpdebench SYSTEM PRIVATE ${LLVM_INCLUDE_DIRS})
separate_arguments(LLVM_DEFINITIONS_LIST NATIVE_COMMAND ${LLVM_DEFINITIONS})
# Same configuration as tpde_llvm_impl (TPDE_ARCH_* are directory-scoped there).
target_compile_definitions(tpdebench PRIVATE ${LLVM_DEFINITIONS_LIST}
    TPDE_ARCH_X86_64 TPDE_ARCH_AARCH64 TPDE_LLVM_ARM64)
target_compile_options(tpdebench PRIVATE -fno-exceptions -fvisibility=hidden -fvisibility-inlines-hidden
    -ffunction-sections -fdata-sections -Wall -Wno-unused-parameter
    -Wno-missing-field-initializers)
target_link_options(tpdebench PRIVATE LINKER:--gc-sections)
llvm_map_components_to_libnames(BENCH_LLVM_LIBS analysis core support targetparser)
target_link_libraries(tpdebench PRIVATE tpde ${BENCH_LLVM_LIBS})
