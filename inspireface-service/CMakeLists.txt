cmake_minimum_required(VERSION 3.20)
project(inspireface_service LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)

set(INSPIREFACE_DIR "" CACHE PATH "Path to InspireFace SDK install/release dir containing include/ and lib/")
set(FACE_SERVICE_FETCH_DEPS ON CACHE BOOL "Fetch cpp-httplib and nlohmann/json with CMake FetchContent")

if(NOT INSPIREFACE_DIR)
  message(FATAL_ERROR "Please set -DINSPIREFACE_DIR=/path/to/InspireFace/sdk")
endif()

if(NOT EXISTS "${INSPIREFACE_DIR}/include/inspireface.h")
  message(FATAL_ERROR "${INSPIREFACE_DIR}/include/inspireface.h not found")
endif()

if(NOT EXISTS "${INSPIREFACE_DIR}/lib/libInspireFace.so")
  message(FATAL_ERROR "${INSPIREFACE_DIR}/lib/libInspireFace.so not found")
endif()

find_package(OpenCV REQUIRED COMPONENTS core imgcodecs imgproc)

if(FACE_SERVICE_FETCH_DEPS)
  include(FetchContent)

  FetchContent_Declare(
    httplib
    URL https://github.com/yhirose/cpp-httplib/archive/refs/tags/v0.18.5.tar.gz
  )
  FetchContent_MakeAvailable(httplib)

  FetchContent_Declare(
    nlohmann_json
    URL https://github.com/nlohmann/json/releases/download/v3.11.3/json.tar.xz
  )
  FetchContent_MakeAvailable(nlohmann_json)
else()
  find_package(nlohmann_json REQUIRED)
  find_path(HTTPLIB_INCLUDE_DIR httplib.h REQUIRED)
endif()

add_executable(face-service src/main.cpp)

target_include_directories(face-service PRIVATE
  "${INSPIREFACE_DIR}/include"
  ${OpenCV_INCLUDE_DIRS}
)

if(NOT FACE_SERVICE_FETCH_DEPS)
  target_include_directories(face-service PRIVATE "${HTTPLIB_INCLUDE_DIR}")
endif()

target_link_directories(face-service PRIVATE "${INSPIREFACE_DIR}/lib")

target_link_libraries(face-service PRIVATE
  InspireFace
  ${OpenCV_LIBS}
  nlohmann_json::nlohmann_json
  pthread
)

if(FACE_SERVICE_FETCH_DEPS)
  target_link_libraries(face-service PRIVATE httplib::httplib)
endif()

set_target_properties(face-service PROPERTIES
  BUILD_RPATH "${INSPIREFACE_DIR}/lib"
  INSTALL_RPATH "$ORIGIN/../lib"
)

install(TARGETS face-service RUNTIME DESTINATION bin)